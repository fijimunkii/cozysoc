package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const splitFirstSeenBatchSQL = `SELECT b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,
 b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT)
 FROM evidence_batch_identity_routes r INDEXED BY evidence_batch_identity_device
 CROSS JOIN evidence_batches b ON b.identity_group=r.group_id
 JOIN evidence_batch_sources s ON s.id=b.source_id
 WHERE r.device_id=? AND s.scope_id=? AND b.first_claim_ns<=? AND b.last_claim_expiry_ns>?
 AND (? OR b.first_claim_ns>? OR (b.first_claim_ns=? AND b.id>?))
 ORDER BY b.first_claim_ns,b.id LIMIT 1`

// Original Device.CreatedAt remains the source's first-seen time unless its
// first observation was split away. In that case find the earliest surviving
// uncorrected claim across both retained storage representations.
func correctedSourceFirstSeen(ctx context.Context, tx *sql.Tx, scopeID, deviceID string, createdAt, asOf, now time.Time, splits deviceSplitMap) (time.Time, error) {
	firstMoved := false
	for _, correction := range splits.bySource[deviceID] {
		for _, route := range splits.linksByObservation[correction.ObservationID] {
			if !route.ObservedAt.After(createdAt) {
				firstMoved = true
			}
		}
	}
	if !firstMoved {
		return createdAt, nil
	}
	var legacy sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(c.observed_at_ns) FROM device_claim_links l
		JOIN identity_claims c ON c.id=l.claim_id
		WHERE l.device_id=? AND c.scope_id=? AND c.observed_at_ns<=? AND c.expires_at_ns>?
		AND l.valid_from_ns<=? AND NOT EXISTS(
			SELECT 1 FROM device_identity_split_links sl WHERE sl.scope_id=? AND sl.link_id=l.id)`,
		deviceID, scopeID, asOf.UnixNano(), now.UnixNano(), asOf.UnixNano(), scopeID).Scan(&legacy); err != nil {
		return time.Time{}, err
	}
	var best time.Time
	if legacy.Valid {
		best = time.Unix(0, legacy.Int64).UTC()
	}
	var cursorTime, cursorBatch int64
	for inspected := 0; ; inspected++ {
		var candidate evidenceBatchCandidate
		err := tx.QueryRowContext(ctx, splitFirstSeenBatchSQL, deviceID, scopeID, asOf.UnixNano(), now.UnixNano(), inspected == 0, cursorTime, cursorTime, cursorBatch).
			Scan(&candidate.ID, &candidate.SourceID, &candidate.GroupID, &candidate.Entries, &candidate.NextExpiry, &candidate.FirstClaim, &candidate.LastClaim, &candidate.LastClaimExpiry, &candidate.Data, &candidate.Sensor, &candidate.Stream)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return time.Time{}, err
		}
		if inspected >= splitProjectionBatchBudget {
			return time.Time{}, ErrEvidenceBatchQueryLimit
		}
		if !best.IsZero() && candidate.FirstClaim >= best.UnixNano() {
			break
		}
		records, err := candidate.records(ctx, tx, scopeID)
		if err != nil {
			return time.Time{}, err
		}
		for _, record := range records {
			claims := map[string]time.Time{}
			for _, retained := range record.Claims {
				if retained.ExpiresAt.After(now) && !retained.Claim.ObservedAt.After(asOf) {
					claims[retained.Claim.ID] = retained.Claim.ObservedAt
				}
			}
			for _, link := range record.Links {
				if link.DeviceID != deviceID || link.ValidFrom.After(asOf) {
					continue
				}
				if _, moved := splits.byLink[link.ID]; moved {
					continue
				}
				if at, ok := claims[link.ClaimID]; ok && (best.IsZero() || at.Before(best)) {
					best = at.UTC()
				}
			}
		}
		cursorTime, cursorBatch = candidate.FirstClaim, candidate.ID
	}
	if best.IsZero() {
		return time.Time{}, ErrDeviceEvidenceNotFound
	}
	return best, nil
}
