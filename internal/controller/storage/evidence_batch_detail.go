package storage

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// GetDeviceEvidenceDetail combines both representations in the caller's fixed
// snapshot. It does not commit, open storage, or fall back on reserved-schema or
// corrupt-data errors. Runtime readers must own a separate read connection.
func (s *MixedIdentitySnapshot) GetDeviceEvidenceDetail(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceEvidenceDetail, error) {
	if s == nil || s.legacy == nil {
		return DeviceEvidenceDetail{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceDetailQuery(query, s.legacy.now)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	legacy, err := getLegacyDeviceEvidenceDetail(ctx, s.legacy.tx, s.legacy.now, q)
	if err != nil && !errors.Is(err, ErrDeviceEvidenceNotFound) {
		return DeviceEvidenceDetail{}, err
	}
	batch, err := getBatchDeviceEvidenceDetail(ctx, s.legacy.tx, s.legacy.now, q)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	if len(legacy.Evidence) == 0 && len(batch.Evidence) == 0 {
		return DeviceEvidenceDetail{}, ErrDeviceEvidenceNotFound
	}
	result := legacy
	if len(result.Evidence) == 0 {
		result.Summary = batch.Summary
	} else if batch.Summary.LastSeen.After(result.Summary.LastSeen) {
		result.Summary.LastSeen = batch.Summary.LastSeen
	}
	result.Evidence = append(result.Evidence, batch.Evidence...)
	// A link ID is globally unique in the legacy contract. Never silently merge
	// conflicting representations into an apparently trustworthy detail response.
	seen := map[string]bool{}
	for _, row := range result.Evidence {
		if seen[row.LinkID] {
			return DeviceEvidenceDetail{}, ErrEvidenceBatchData
		}
		seen[row.LinkID] = true
	}
	sortDeviceEvidenceRows(result.Evidence)
	return result.page(q.Limit), nil
}

func sortDeviceEvidenceRows(rows []deviceEvidenceRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.Evidence.ObservedAt.Equal(b.Evidence.ObservedAt) {
			return a.Evidence.ObservedAt.After(b.Evidence.ObservedAt)
		}
		if a.ClaimID != b.ClaimID {
			return a.ClaimID < b.ClaimID
		}
		return a.LinkID < b.LinkID
	})
}

const batchDeviceDetailCandidateSQL = `SELECT d.id,d.user_label,d.created_at_ns,d.retired_at_ns,
 b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT)
 FROM evidence_batches b
 JOIN evidence_batch_sources s ON s.id=b.source_id
 JOIN devices d ON d.id=?
 WHERE b.identity_group IN (SELECT group_id FROM evidence_batch_identity_routes WHERE device_id=d.id)
 AND s.scope_id=? AND b.first_claim_ns<=? AND b.last_claim_expiry_ns>?
 AND (d.retired_at_ns IS NULL OR d.retired_at_ns>=?)
 AND (? OR b.last_claim_ns<? OR (b.last_claim_ns=? AND b.id<?))
 ORDER BY b.last_claim_ns DESC,b.id DESC LIMIT 1`

func getBatchDeviceEvidenceDetail(ctx context.Context, tx *sql.Tx, now time.Time, q DeviceEvidenceDetailQuery) (deviceEvidenceDetailRows, error) {
	result := deviceEvidenceDetailRows{}
	var cursorTime, cursorBatch int64
	seenLinks := map[string]bool{}
	for inspected := 0; ; inspected++ {
		var device domain.Device
		var label sql.NullString
		var created int64
		var retired sql.NullInt64
		var candidate evidenceBatchCandidate
		err := tx.QueryRowContext(ctx, batchDeviceDetailCandidateSQL, q.DeviceID, q.ScopeID, q.AsOf.UnixNano(), now.UnixNano(), q.AsOf.UnixNano(), inspected == 0, cursorTime, cursorTime, cursorBatch).Scan(&device.ID, &label, &created, &retired, &candidate.ID, &candidate.SourceID, &candidate.GroupID, &candidate.Entries, &candidate.NextExpiry, &candidate.FirstClaim, &candidate.LastClaim, &candidate.LastClaimExpiry, &candidate.Data, &candidate.Sensor, &candidate.Stream)
		if errors.Is(err, sql.ErrNoRows) {
			return result, nil
		}
		if err != nil {
			return deviceEvidenceDetailRows{}, err
		}
		if inspected >= evidenceBatchIdentityMaxCandidates {
			return deviceEvidenceDetailRows{}, ErrEvidenceBatchQueryLimit
		}
		records, err := candidate.records(ctx, tx, q.ScopeID)
		if err != nil {
			return deviceEvidenceDetailRows{}, err
		}
		// Original claim times cannot exceed the verified batch's stored upper bound.
		// Keep all ties: claim IDs, not physical batch order, decide evidence ordering.
		if len(result.Evidence) > q.Limit && candidate.LastClaim < result.Evidence[q.Limit].Evidence.ObservedAt.UnixNano() {
			return result, nil
		}
		device.UserLabel = label.String
		device.CreatedAt = time.Unix(0, created).UTC()
		if retired.Valid {
			at := time.Unix(0, retired.Int64).UTC()
			device.RetiredAt = &at
		}
		if err := domain.ValidateDevice(device); err != nil {
			return deviceEvidenceDetailRows{}, err
		}
		for _, record := range records {
			claims := map[string]RetainedIdentityClaim{}
			for _, claim := range record.Claims {
				claims[claim.Claim.ID] = claim
			}
			for _, link := range record.Links {
				if link.DeviceID != q.DeviceID || link.ValidFrom.After(q.AsOf) {
					continue
				}
				retained, ok := claims[link.ClaimID]
				if !ok {
					return deviceEvidenceDetailRows{}, ErrEvidenceBatchData
				}
				claim := retained.Claim
				if !retained.ExpiresAt.After(now) || claim.ObservedAt.After(q.AsOf) {
					continue
				}
				if seenLinks[link.ID] {
					return deviceEvidenceDetailRows{}, ErrEvidenceBatchData
				}
				seenLinks[link.ID] = true
				value, err := domain.NormalizeClaimValue(claim.Kind, claim.Value)
				if err != nil {
					return deviceEvidenceDetailRows{}, err
				}
				item := DeviceIdentityEvidence{Kind: claim.Kind, Value: value, ObservedAt: claim.ObservedAt, ClaimValidUntil: claim.ValidUntil, ClaimConfidence: claim.Confidence, SourceSensorID: claim.SourceSensorID, LinkValidUntil: link.ValidUntil, LinkConfidence: link.Confidence, Authority: link.Authority, Reason: link.Reason}
				if record.Observation != nil && record.ObservationExpiresAt.After(now) && claim.SourceObservationID == record.Observation.ID {
					o := record.Observation
					item.Observation = &DeviceEvidenceObservation{ID: o.ID, SensorID: o.SensorID, Kind: o.Kind, SourceStream: o.SourceStream, IngestedAt: o.IngestedAt, Attribution: o.Attribution}
				}
				if result.Summary.Device.ID == "" {
					result.Summary = DeviceEvidenceSummary{Device: device, FirstSeen: device.CreatedAt, LastSeen: claim.ObservedAt}
				} else if claim.ObservedAt.After(result.Summary.LastSeen) {
					result.Summary.LastSeen = claim.ObservedAt
				}
				result.Evidence = append(result.Evidence, deviceEvidenceRow{Evidence: item, ClaimID: claim.ID, LinkID: link.ID})
			}
		}
		sortDeviceEvidenceRows(result.Evidence)
		if len(result.Evidence) > q.Limit+1 {
			result.Evidence = result.Evidence[:q.Limit+1]
		}
		cursorTime, cursorBatch = candidate.LastClaim, candidate.ID
	}
}
