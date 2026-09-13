package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type identityQueryWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// LegacyRepairStore confines reconciliation to the original retained observation
// and the caller's transaction. It never replaces the observation or commits.
type LegacyRepairStore struct {
	tx        *sql.Tx
	original  domain.Observation
	reader    *MixedIdentitySnapshot
	retention map[domain.RetentionClass]time.Duration
	claims    map[string]bool
}

// NewLegacyRepairStore resolves the immutable source tuple, ignoring retry IDs,
// payloads and timestamps. Missing/expired originals return false without repair.
// The owner must roll back any error and use a dedicated transaction connection.
func NewLegacyRepairStore(ctx context.Context, tx *sql.Tx, limits Limits, retry domain.Observation) (*LegacyRepairStore, bool, error) {
	if tx == nil {
		return nil, false, ErrEvidenceBatchData
	}
	if err := domain.ValidateObservation(retry); err != nil {
		return nil, false, err
	}
	limits, err := normalizeLimits(limits)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	var scope string
	if err := tx.QueryRowContext(ctx, `SELECT scope_id FROM sensors WHERE id=?`, retry.SensorID).Scan(&scope); err != nil {
		return nil, false, err
	}
	if scope != retry.ScopeID {
		return nil, false, ErrEvidenceBatchData
	}
	// A one-byte-over-limit prefix bounds text allocation while ensuring domain
	// validation rejects oversized values instead of accepting truncated evidence.
	text := func(column string) string { return "CAST(substr(CAST(" + column + " AS BLOB),1,513) AS TEXT)" }
	columns := []string{text("id"), text("scope_id"), text("sensor_id"), text("kind"), text("source_stream"), text("source_key"), text("source_event_id"), "source_time_ns", "ingested_at_ns", "schema_version", "confidence", text("attribution"), "CASE WHEN length(CAST(payload AS BLOB))<=65536 THEN payload ELSE NULL END", text("retention_class")}
	row := tx.QueryRowContext(ctx, "SELECT "+strings.Join(columns, ",")+` FROM observations WHERE scope_id=? AND sensor_id=? AND source_stream=? AND source_key=? AND expires_at_ns>?`, retry.ScopeID, retry.SensorID, retry.SourceStream, retry.SourceKey, now.UnixNano())
	original, err := scanObservation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := domain.ValidateObservation(original); err != nil {
		return nil, false, err
	}
	reader, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		return nil, false, err
	}
	return &LegacyRepairStore{tx: tx, original: original, reader: reader, retention: limits.Retention, claims: map[string]bool{}}, true, nil
}
func (s *LegacyRepairStore) Observation() domain.Observation {
	return cloneBatchObservation(s.original)
}
func (s *LegacyRepairStore) FindRecentDevicesByClaim(ctx context.Context, scope string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
	if scope != s.original.ScopeID {
		return nil, ErrEvidenceBatchData
	}
	return s.reader.FindRecentDevicesByClaim(ctx, scope, kind, value, since, until)
}
func (s *LegacyRepairStore) EnsureDevice(ctx context.Context, d domain.Device) error {
	at := s.original.IngestedAt
	if s.original.SourceTime != nil {
		at = *s.original.SourceTime
	}
	if !d.CreatedAt.Equal(at) || d.UserLabel != "" || d.RetiredAt != nil {
		return ErrEvidenceBatchData
	}
	return ensureIdentityDevice(ctx, s.tx, d)
}
func (s *LegacyRepairStore) EnsureIdentityClaim(ctx context.Context, c domain.IdentityClaim) (string, error) {
	if c.ScopeID != s.original.ScopeID || c.SourceSensorID != s.original.SensorID || c.SourceObservationID != s.original.ID || len(s.claims) >= EvidenceBatchMaxAssociations {
		return "", ErrEvidenceBatchData
	}
	id, err := ensureIdentityClaim(ctx, s.tx, c, func(class domain.RetentionClass) (int64, error) {
		duration, ok := s.retention[class]
		if !ok || duration <= 0 {
			return 0, ErrEvidenceBatchData
		}
		at := time.Now().UTC().Add(duration)
		if !batchTimeFits(at) {
			return 0, ErrEvidenceBatchData
		}
		return at.UnixNano(), nil
	})
	if err == nil {
		s.claims[id] = true
	}
	return id, err
}
func (s *LegacyRepairStore) EnsureDeviceClaimLink(ctx context.Context, l domain.DeviceClaimLink) error {
	if l.EvidenceObservationID != s.original.ID || !s.claims[l.ClaimID] {
		return ErrEvidenceBatchData
	}
	return ensureIdentityLink(ctx, s.tx, l)
}
