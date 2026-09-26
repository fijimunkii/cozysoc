package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

var ErrEvidenceBatchQueryLimit = errors.New("batch identity query work limit exceeded")

const evidenceBatchIdentityMaxCandidates = 100

// MixedIdentitySnapshot combines legacy and reserved batch evidence in one
// transaction. Reserved schema must exist; it never falls back on schema errors.
// This is not wired to live ingestion or migration.
type MixedIdentitySnapshot struct{ legacy *LegacyIdentitySnapshot }

func NewMixedIdentitySnapshot(tx *sql.Tx, now time.Time) (*MixedIdentitySnapshot, error) {
	legacy, err := NewLegacyIdentitySnapshot(tx, now)
	if err != nil {
		return nil, err
	}
	return &MixedIdentitySnapshot{legacy}, nil
}
func (s *MixedIdentitySnapshot) FindRecentDevicesByClaim(ctx context.Context, scope string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
	if s == nil || s.legacy == nil {
		return nil, fmt.Errorf("identity snapshot is unavailable")
	}
	merges, err := loadDeviceMerges(ctx, s.legacy.tx, scope)
	if err != nil {
		return nil, err
	}
	limit := 3
	if len(merges.bySource) != 0 {
		limit = MaxDeviceMergesPerScope + 2
	}
	legacy, err := findRecentDevicesByClaim(ctx, s.legacy.tx, s.legacy.now, scope, kind, value, since, until, limit)
	if err != nil {
		return nil, err
	}
	batch, err := findRecentBatchDevicesByClaimLimit(ctx, s.legacy.tx, s.legacy.now, scope, kind, value, since, until, limit)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.Device{}
	for _, d := range append(legacy, batch...) {
		id := merges.canonical(d.ID)
		if id == d.ID {
			byID[id] = d
			continue
		}
		if _, exists := byID[id]; exists {
			continue
		}
		var target domain.Device
		var label sql.NullString
		var created int64
		var retired sql.NullInt64
		if err := s.legacy.tx.QueryRowContext(ctx, `SELECT id,user_label,created_at_ns,retired_at_ns FROM devices WHERE id=?`, id).Scan(&target.ID, &label, &created, &retired); err != nil {
			return nil, err
		}
		target.UserLabel = label.String
		target.CreatedAt = time.Unix(0, created).UTC()
		if retired.Valid {
			at := time.Unix(0, retired.Int64).UTC()
			target.RetiredAt = &at
		}
		if target.RetiredAt != nil && target.RetiredAt.Before(until) {
			return nil, ErrEvidenceBatchData
		}
		byID[id] = target
	}
	devices := make([]domain.Device, 0, len(byID))
	for _, d := range byID {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	if len(devices) > 3 {
		devices = devices[:3]
	}
	return devices, nil
}

const batchIdentityCandidateSQL = `SELECT d.id,d.user_label,d.created_at_ns,d.retired_at_ns,
 b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,s.sensor_id,s.stream
 FROM evidence_batch_identity_routes r
 JOIN evidence_batch_identity_groups g ON g.id=r.group_id
 JOIN evidence_batch_sources s ON s.id=g.source_id
 JOIN evidence_batches b ON b.identity_group=g.id AND b.source_id=g.source_id
 JOIN devices d ON d.id=r.device_id
 WHERE r.kind=? AND r.value=? AND s.scope_id=?
 AND b.first_claim_ns<=? AND b.last_claim_ns>=? AND b.last_claim_expiry_ns>?
 AND (d.retired_at_ns IS NULL OR d.retired_at_ns>=?)
 AND (r.device_id>? OR (r.device_id=? AND (r.group_id>? OR (r.group_id=? AND b.id<?))))
 ORDER BY r.device_id,r.group_id,b.id DESC LIMIT 1`

func findRecentBatchDevicesByClaim(ctx context.Context, tx *sql.Tx, now time.Time, scope string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
	return findRecentBatchDevicesByClaimLimit(ctx, tx, now, scope, kind, value, since, until, 3)
}

func findRecentBatchDevicesByClaimLimit(ctx context.Context, tx *sql.Tx, now time.Time, scope string, kind domain.ClaimKind, value string, since, until time.Time, limit int) ([]domain.Device, error) {
	if validateQueryID("scope", scope) != nil || !batchTimeFits(now) || !batchTimeFits(since) || !batchTimeFits(until) || until.Before(since) || until.Sub(since) > MaxQueryWindow {
		return nil, ErrEvidenceBatchData
	}
	normalized, err := domain.NormalizeClaimValue(kind, value)
	if err != nil {
		return nil, err
	}
	devices := make([]domain.Device, 0, limit)
	cursorDevice := ""
	var cursorGroup int64
	cursorBatch := int64(math.MaxInt64)
	for inspected := 0; ; inspected++ {
		var d domain.Device
		var label sql.NullString
		var created int64
		var retired sql.NullInt64
		var batch, source, group, nextExpiry, first, last, expiry int64
		var entries int
		var data []byte
		var sensor, stream string
		err := tx.QueryRowContext(ctx, batchIdentityCandidateSQL, kind, normalized, scope, until.UnixNano(), since.UnixNano(), now.UnixNano(), until.UnixNano(), cursorDevice, cursorDevice, cursorGroup, cursorGroup, cursorBatch).Scan(&d.ID, &label, &created, &retired, &batch, &source, &group, &entries, &nextExpiry, &first, &last, &expiry, &data, &sensor, &stream)
		if errors.Is(err, sql.ErrNoRows) {
			return devices, nil
		}
		if err != nil {
			return nil, err
		}
		if inspected >= evidenceBatchIdentityMaxCandidates {
			return nil, ErrEvidenceBatchQueryLimit
		}
		records, err := DecodeEvidenceBatch(data)
		if err != nil {
			return nil, err
		}
		if len(records) != entries || nextEvidenceBatchExpiry(records) != nextExpiry {
			return nil, ErrEvidenceBatchData
		}
		if err := validateEvidenceBatchClaimTimes(records); err != nil {
			return nil, err
		}
		f, l, e := evidenceBatchClaimBounds(records)
		if first != f || last != l || expiry != e {
			return nil, ErrEvidenceBatchData
		}
		for _, r := range records {
			if err := validateRetainedBatchBundle(r, scope, sensor, stream); err != nil {
				return nil, err
			}
		}
		if err := validateEvidenceBatchIdentityGroup(ctx, tx, group, source, records); err != nil {
			return nil, err
		}
		cursorDevice, cursorGroup, cursorBatch = d.ID, group, batch
		matched := false
		for _, r := range records {
			claims := map[string]bool{}
			for _, c := range r.Claims {
				v, err := domain.NormalizeClaimValue(c.Claim.Kind, c.Claim.Value)
				if err != nil {
					return nil, err
				}
				if c.Claim.Kind == kind && v == normalized && !c.Claim.ObservedAt.Before(since) && !c.Claim.ObservedAt.After(until) && c.ExpiresAt.After(now) {
					claims[c.Claim.ID] = true
				}
			}
			for _, link := range r.Links {
				if link.DeviceID == d.ID && claims[link.ClaimID] {
					matched = true
				}
			}
		}
		if !matched {
			continue
		}
		d.UserLabel = label.String
		d.CreatedAt = time.Unix(0, created).UTC()
		if retired.Valid {
			at := time.Unix(0, retired.Int64).UTC()
			d.RetiredAt = &at
		}
		devices = append(devices, d)
		if len(devices) == limit {
			return devices, nil
		}
		// A verified device is already represented; skip its remaining batches.
		cursorGroup, cursorBatch = math.MaxInt64, 0
	}
}
