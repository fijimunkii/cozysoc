package storage

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// One page may contain 200 devices plus its lookahead. The total budget also
// bounds stale or ineligible candidate batches; exhaustion returns no page.
const evidenceBatchDeviceListMaxCandidates = 1024

// ListDeviceEvidence combines retained legacy/batch summaries in the caller's
// snapshot. It preserves device-ID pagination and derives last-seen from original
// claims, never a routing bound or an inferred continuous presence interval.
func (s *MixedIdentitySnapshot) ListDeviceEvidence(ctx context.Context, query DeviceEvidenceQuery) (DeviceEvidencePage, error) {
	if s == nil || s.legacy == nil {
		return DeviceEvidencePage{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceEvidenceQuery(query, s.legacy.now)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	legacy, err := listLegacyDeviceEvidence(ctx, s.legacy.tx, s.legacy.now, q)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	throughID := ""
	if len(legacy) > q.Limit {
		throughID = legacy[q.Limit].Device.ID
	}
	batch, err := listBatchDeviceEvidence(ctx, s.legacy.tx, s.legacy.now, q, throughID)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	byID := map[string]DeviceEvidenceSummary{}
	for _, item := range append(legacy, batch...) {
		if previous, ok := byID[item.Device.ID]; !ok || item.LastSeen.After(previous.LastSeen) {
			byID[item.Device.ID] = item
		}
	}
	devices := make([]DeviceEvidenceSummary, 0, len(byID))
	for _, item := range byID {
		devices = append(devices, item)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Device.ID < devices[j].Device.ID })
	return deviceEvidencePage(devices, q.Limit), nil
}

// Keep devices before routes and batches in the SQLite join order. Otherwise
// the planner can scan every retained batch for each cursor step. The explicit
// lower device bound permits an ordered primary-key range scan.
const batchDeviceListCandidateSQL = `SELECT d.id,d.user_label,d.created_at_ns,d.retired_at_ns,
 b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT)
 FROM devices d
 CROSS JOIN evidence_batch_identity_routes r INDEXED BY evidence_batch_identity_device ON r.device_id=d.id
 CROSS JOIN evidence_batches b ON b.identity_group=r.group_id
 JOIN evidence_batch_sources s ON s.id=b.source_id
 WHERE s.scope_id=? AND b.first_claim_ns<=? AND b.last_claim_expiry_ns>?
 AND (d.retired_at_ns IS NULL OR d.retired_at_ns>=?)
 AND d.id>=?
 AND (d.id>? OR (d.id=? AND ? AND (b.last_claim_ns<? OR (b.last_claim_ns=? AND b.id<?))))
 AND (?='' OR d.id<=?)
 ORDER BY d.id,b.last_claim_ns DESC,b.id DESC LIMIT 1`

func listBatchDeviceEvidence(ctx context.Context, tx *sql.Tx, now time.Time, q DeviceEvidenceQuery, throughID string) ([]DeviceEvidenceSummary, error) {
	devices := make([]DeviceEvidenceSummary, 0, q.Limit+1)
	cursorDevice := q.AfterID
	var cursorTime, cursorBatch int64
	allowSame := false
	for inspected := 0; ; inspected++ {
		var device domain.Device
		var label sql.NullString
		var created int64
		var retired sql.NullInt64
		var candidate evidenceBatchCandidate
		err := tx.QueryRowContext(ctx, batchDeviceListCandidateSQL, q.ScopeID, q.AsOf.UnixNano(), now.UnixNano(), q.AsOf.UnixNano(), cursorDevice, cursorDevice, cursorDevice, allowSame, cursorTime, cursorTime, cursorBatch, throughID, throughID).Scan(&device.ID, &label, &created, &retired, &candidate.ID, &candidate.SourceID, &candidate.GroupID, &candidate.Entries, &candidate.NextExpiry, &candidate.FirstClaim, &candidate.LastClaim, &candidate.LastClaimExpiry, &candidate.Data, &candidate.Sensor, &candidate.Stream)
		if errors.Is(err, sql.ErrNoRows) {
			return devices, nil
		}
		if err != nil {
			return nil, err
		}
		if inspected >= evidenceBatchDeviceListMaxCandidates {
			return nil, ErrEvidenceBatchQueryLimit
		}
		records, err := candidate.records(ctx, tx, q.ScopeID)
		if err != nil {
			return nil, err
		}
		device.UserLabel = label.String
		device.CreatedAt = time.Unix(0, created).UTC()
		if retired.Valid {
			at := time.Unix(0, retired.Int64).UTC()
			device.RetiredAt = &at
		}
		if err := domain.ValidateDevice(device); err != nil {
			return nil, err
		}
		latest := DeviceEvidenceSummary{Device: device, FirstSeen: device.CreatedAt}
		existing := len(devices) > 0 && devices[len(devices)-1].Device.ID == device.ID
		if existing {
			latest = devices[len(devices)-1]
		}
		for _, record := range records {
			claims := map[string]time.Time{}
			for _, c := range record.Claims {
				if c.ExpiresAt.After(now) && !c.Claim.ObservedAt.After(q.AsOf) {
					claims[c.Claim.ID] = c.Claim.ObservedAt.UTC()
				}
			}
			for _, link := range record.Links {
				// Legacy device-list membership intentionally does not filter link start or
				// presence validity. Detail and activity have their own stricter contracts.
				if at, ok := claims[link.ClaimID]; ok && link.DeviceID == device.ID && (latest.LastSeen.IsZero() || at.After(latest.LastSeen)) {
					latest.LastSeen = at
				}
			}
		}
		if !latest.LastSeen.IsZero() {
			if existing {
				devices[len(devices)-1] = latest
			} else {
				devices = append(devices, latest)
			}
			// The extra ID proves pagination; its summary is never returned to callers.
			if len(devices) > q.Limit {
				return devices, nil
			}
		}
		cursorDevice, cursorTime, cursorBatch = device.ID, candidate.LastClaim, candidate.ID
		allowSame = true
		if !latest.LastSeen.IsZero() && (latest.LastSeen.UnixNano() >= candidate.LastClaim || latest.LastSeen.Equal(q.AsOf)) {
			allowSame = false
		}
	}
}
