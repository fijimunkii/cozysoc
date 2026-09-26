package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// EvidenceBatchPlan is a producer decision, without caller-selected expiry or
// changes to the original observation. The planner is a trusted controller
// adapter, never a callback chosen through a network or UI request.
type EvidenceBatchPlan struct {
	NewDevice *domain.Device
	Claims    []domain.IdentityClaim
	Links     []domain.DeviceClaimLink
	// ArrivalFinding is an informational result of a newly observed Device
	// Watch identity, committed with its source observation and device.
	ArrivalFinding *domain.Finding
}
type EvidenceBatchPlanner func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error)

// ErrEvidenceBatchLegacyReplay requires the owner to use the legacy replay/repair
// path with the originally stored observation. A legacy observation can predate
// completed reconciliation, so it must not be treated as a complete atomic batch.
var ErrEvidenceBatchLegacyReplay = errors.New("legacy observation replay requires compatibility handling")

type EvidenceBatchStager struct {
	retention map[domain.RetentionClass]time.Duration
	planner   EvidenceBatchPlanner
	now       func() time.Time
}

// NewEvidenceBatchStager copies the configured retention policy. The connection
// owner still configures quota, durability and foreign keys. This constructor
// does not open storage or install the schema.
func NewEvidenceBatchStager(limits Limits, planner EvidenceBatchPlanner) (*EvidenceBatchStager, error) {
	if planner == nil {
		return nil, fmt.Errorf("batch planner is required")
	}
	normalized, err := normalizeLimits(limits)
	if err != nil {
		return nil, err
	}
	return &EvidenceBatchStager{retention: normalized.Retention, planner: planner, now: time.Now}, nil
}

// Stage checks replay in both representations before planning, then stages the
// entire decision on the same exclusively owned transaction. The owner MUST
// rollback on error and acknowledge only after commit. Never use Store.conn.
// A batch replay returns an empty record and false without invoking the planner,
// assigning expiry or changing either representation. Legacy replay returns
// ErrEvidenceBatchLegacyReplay so the owner cannot silently skip legacy repair.
// Expired but unpruned observations still suppress new insertion, matching
// legacy ingestion.
func (s *EvidenceBatchStager) Stage(ctx context.Context, tx *sql.Tx, o domain.Observation) (EvidenceBatchRecord, bool, error) {
	empty := EvidenceBatchRecord{}
	if s == nil || s.planner == nil || s.now == nil || tx == nil {
		return empty, false, ErrEvidenceBatchData
	}
	if err := domain.ValidateObservation(o); err != nil {
		return empty, false, err
	}
	o = cloneBatchObservation(o)
	replay, err := evidenceBatchObservationReplay(ctx, tx, o)
	if err != nil || replay {
		return empty, false, err
	}
	reader, err := NewMixedIdentitySnapshot(tx, s.now().UTC())
	if err != nil {
		return empty, false, err
	}
	plan, err := s.planner(ctx, reader, cloneBatchObservation(o))
	if err != nil {
		return empty, false, err
	}
	if len(plan.Claims) > EvidenceBatchMaxAssociations || len(plan.Links) > EvidenceBatchMaxAssociations {
		return empty, false, ErrEvidenceBatchLimit
	}
	expiry, err := s.expiry(o.Retention)
	if err != nil {
		return empty, false, err
	}
	record := EvidenceBatchRecord{Observation: &o, ObservationExpiresAt: &expiry, Links: plan.Links}
	for _, c := range plan.Claims {
		expiry, err := s.expiry(c.Retention)
		if err != nil {
			return empty, false, err
		}
		record.Claims = append(record.Claims, RetainedIdentityClaim{Claim: c, ExpiresAt: expiry})
	}
	if err := validateCompleteBatchBundle(record); err != nil {
		return empty, false, err
	}
	if err := validateEvidenceBatchClaimTimes([]EvidenceBatchRecord{record}); err != nil {
		return empty, false, err
	}
	if _, err := EncodeEvidenceBatch([]EvidenceBatchRecord{record}); err != nil {
		return empty, false, err
	}
	if plan.NewDevice != nil {
		d := *plan.NewDevice
		if err := domain.ValidateDevice(d); err != nil {
			return empty, false, err
		}
		used := false
		for _, l := range plan.Links {
			if l.DeviceID == d.ID {
				used = true
			}
		}
		if !used || d.UserLabel != "" || d.RetiredAt != nil || !batchTimeFits(d.CreatedAt) {
			return empty, false, ErrEvidenceBatchData
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id,created_at_ns) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, d.ID, d.CreatedAt.UnixNano()); err != nil {
			return empty, false, err
		}
		var created int64
		if err := tx.QueryRowContext(ctx, `SELECT created_at_ns FROM devices WHERE id=?`, d.ID).Scan(&created); err != nil {
			return empty, false, err
		}
		if created != d.CreatedAt.UnixNano() {
			return empty, false, ErrEvidenceBatchData
		}
	}
	// No route may introduce a nonexistent device. The live producer supplies
	// frozen derived IDs; arbitrary batch claim/link IDs are not exposed to it.
	checked := map[string]bool{}
	for _, l := range plan.Links {
		if !checked[l.DeviceID] {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE id=?`, l.DeviceID).Scan(&exists); err != nil {
				return empty, false, err
			}
			checked[l.DeviceID] = true
		}
	}
	inserted, err := appendEvidenceBatch(ctx, tx, record)
	if err != nil {
		return empty, false, err
	}
	// Replay was already checked in this snapshot. A newly appearing replay means
	// the trusted planner wrote unexpectedly; require rollback of all its changes.
	if !inserted {
		return empty, false, ErrEvidenceBatchData
	}
	if plan.ArrivalFinding != nil {
		f := *plan.ArrivalFinding
		if plan.NewDevice == nil || f.Category != "new-device" || f.Severity != "informational" ||
			f.Confidence != nil || f.ScopeID != o.ScopeID || f.Retention != o.Retention ||
			len(f.EvidenceObservationIDs) != 1 || f.EvidenceObservationIDs[0] != o.ID ||
			!f.ObservedAt.Equal(plan.NewDevice.CreatedAt) || !f.CreatedAt.Equal(o.IngestedAt) ||
			domain.ValidateFinding(f) != nil {
			return empty, false, ErrEvidenceBatchData
		}
		if err := insertFindingTx(ctx, tx, f, expiry.UnixNano()); err != nil {
			return empty, false, err
		}
	}
	return record, true, nil
}

func (s *EvidenceBatchStager) expiry(class domain.RetentionClass) (time.Time, error) {
	duration, ok := s.retention[class]
	if !ok || duration <= 0 {
		return time.Time{}, ErrEvidenceBatchData
	}
	now := s.now().UTC()
	expiry := now.Add(duration)
	if !batchTimeFits(now) || !batchTimeFits(expiry) {
		return time.Time{}, ErrEvidenceBatchData
	}
	return expiry, nil
}

func cloneBatchObservation(o domain.Observation) domain.Observation {
	o.Payload = append([]byte(nil), o.Payload...)
	if o.SourceTime != nil {
		at := *o.SourceTime
		o.SourceTime = &at
	}
	if o.Confidence != nil {
		value := *o.Confidence
		o.Confidence = &value
	}
	return o
}

func evidenceBatchObservationReplay(ctx context.Context, tx *sql.Tx, o domain.Observation) (bool, error) {
	var sensorScope string
	if err := tx.QueryRowContext(ctx, `SELECT scope_id FROM sensors WHERE id=?`, o.SensorID).Scan(&sensorScope); err != nil {
		return false, err
	}
	if sensorScope != o.ScopeID {
		return false, ErrEvidenceBatchData
	}
	// Probe both schemas even on a legacy replay: an incomplete installed schema
	// is an integration error, not permission to bypass the configured path.
	var batchSource sql.NullInt64
	var batchScope string
	err := tx.QueryRowContext(ctx, `SELECT id,scope_id FROM evidence_batch_sources WHERE sensor_id=? AND stream=?`, o.SensorID, o.SourceStream).Scan(&batchSource, &batchScope)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && batchScope != o.ScopeID {
		return false, ErrEvidenceBatchData
	}
	var legacyScope string
	err = tx.QueryRowContext(ctx, `SELECT scope_id FROM observations WHERE sensor_id=? AND source_stream=? AND source_key=?`, o.SensorID, o.SourceStream, o.SourceKey).Scan(&legacyScope)
	legacyReplay := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if legacyReplay && legacyScope != o.ScopeID {
		return false, ErrEvidenceBatchData
	}
	key := packedEvidenceKey(o.SourceKey, "")
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM evidence_batch_lookup WHERE id=? AND source_id=? AND source_key IS NULL
 UNION ALL SELECT 1 FROM evidence_batch_lookup WHERE source_id=? AND source_key=? LIMIT 1`, key, batchSource, batchSource, key).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if legacyReplay {
		return false, ErrEvidenceBatchLegacyReplay
	}
	if err == nil {
		return true, nil
	}
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM observations WHERE id=? UNION ALL SELECT 1 FROM evidence_batch_lookup WHERE id=? LIMIT 1`, o.ID, packedEvidenceKey(o.ID, "obs.dw.")).Scan(&exists)
	if err == nil {
		return false, ErrEvidenceBatchData
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return false, nil
}
