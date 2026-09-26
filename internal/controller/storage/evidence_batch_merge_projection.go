package storage

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const mergeProjectionBatchBudget = 256

func mergedDeviceSummary(previous, next DeviceEvidenceSummary) DeviceEvidenceSummary {
	if previous.Device.ID == "" {
		return next
	}
	if previous.FirstSeen.IsZero() || next.FirstSeen.Before(previous.FirstSeen) {
		previous.FirstSeen = next.FirstSeen
	}
	if next.LastSeen.After(previous.LastSeen) {
		previous.LastSeen = next.LastSeen
	}
	return previous
}

func loadMergeTargetDevice(ctx context.Context, tx *sql.Tx, id string, asOf time.Time) (domain.Device, error) {
	var d domain.Device
	var label sql.NullString
	var created int64
	var retired sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT id,user_label,created_at_ns,retired_at_ns FROM devices WHERE id=?`, id).
		Scan(&d.ID, &label, &created, &retired); err != nil {
		return d, err
	}
	d.UserLabel = label.String
	d.CreatedAt = time.Unix(0, created).UTC()
	if retired.Valid {
		at := time.Unix(0, retired.Int64).UTC()
		d.RetiredAt = &at
		if at.Before(asOf) {
			return domain.Device{}, ErrEvidenceBatchData
		}
	}
	if err := domain.ValidateDevice(d); err != nil {
		return domain.Device{}, err
	}
	return d, nil
}

// Pagination is over corrected device IDs. At most 64 source IDs can disappear
// from an original page; scanning that many extra original IDs plus one lookahead
// is sufficient to form one corrected page. Source summaries are checked by the
// existing exact-evidence reader before they can contribute to a target.
func (s *MixedIdentitySnapshot) listMergedDeviceEvidence(ctx context.Context, query DeviceEvidenceQuery) (DeviceEvidencePage, error) {
	if s == nil || s.legacy == nil {
		return DeviceEvidencePage{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceEvidenceQuery(query, s.legacy.now)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	merges, err := loadDeviceMerges(ctx, s.legacy.tx, q.ScopeID)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	if len(merges.bySource) == 0 {
		return s.listOriginalDeviceEvidence(ctx, q)
	}
	byID := map[string]DeviceEvidenceSummary{}
	remaining := mergeProjectionBatchBudget
	for targetID, sources := range merges.sourcesByTarget {
		if targetID <= q.AfterID {
			continue
		}
		for _, sourceID := range sources {
			detail, err := s.getOriginalDeviceEvidenceDetailBudget(ctx, DeviceEvidenceDetailQuery{ScopeID: q.ScopeID, DeviceID: sourceID, AsOf: q.AsOf, Limit: 1}, &remaining)
			if errors.Is(err, ErrDeviceEvidenceNotFound) {
				continue
			}
			if err != nil {
				return DeviceEvidencePage{}, err
			}
			target, err := loadMergeTargetDevice(ctx, s.legacy.tx, targetID, q.AsOf)
			if err != nil {
				return DeviceEvidencePage{}, err
			}
			summary := detail.Summary
			summary.Device = target
			byID[targetID] = mergedDeviceSummary(byID[targetID], summary)
		}
	}
	pageSize := min(MaxQueryLimit, q.Limit+len(merges.bySource)+1)
	cursor := q.AfterID
	nonSource := 0
	for nonSource <= q.Limit {
		page, err := s.listOriginalDeviceEvidence(ctx, DeviceEvidenceQuery{ScopeID: q.ScopeID, AsOf: q.AsOf, AfterID: cursor, Limit: pageSize})
		if err != nil {
			return DeviceEvidencePage{}, err
		}
		for _, summary := range page.Devices {
			if merges.bySource[summary.Device.ID] != "" {
				continue
			}
			nonSource++
			byID[summary.Device.ID] = mergedDeviceSummary(byID[summary.Device.ID], summary)
		}
		if page.NextID == "" || nonSource > q.Limit {
			break
		}
		cursor = page.NextID
	}
	result := make([]DeviceEvidenceSummary, 0, len(byID))
	for _, summary := range byID {
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Device.ID < result[j].Device.ID })
	return deviceEvidencePage(result, q.Limit), nil
}

func (s *MixedIdentitySnapshot) getMergedDeviceEvidenceDetail(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceEvidenceDetail, error) {
	if s == nil || s.legacy == nil {
		return DeviceEvidenceDetail{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceDetailQuery(query, s.legacy.now)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	merges, err := loadDeviceMerges(ctx, s.legacy.tx, q.ScopeID)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	if merges.bySource[q.DeviceID] != "" {
		return DeviceEvidenceDetail{}, ErrDeviceEvidenceNotFound
	}
	sources := merges.sourcesByTarget[q.DeviceID]
	if len(sources) == 0 {
		return s.getOriginalDeviceEvidenceDetail(ctx, q)
	}
	target, err := loadMergeTargetDevice(ctx, s.legacy.tx, q.DeviceID, q.AsOf)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	result := DeviceEvidenceDetail{Summary: DeviceEvidenceSummary{Device: target}, Evidence: []DeviceIdentityEvidence{}}
	remaining := mergeProjectionBatchBudget
	for _, origin := range append([]string{q.DeviceID}, sources...) {
		part, err := s.getOriginalDeviceEvidenceDetailBudget(ctx, DeviceEvidenceDetailQuery{ScopeID: q.ScopeID, DeviceID: origin, AsOf: q.AsOf, Limit: q.Limit}, &remaining)
		if errors.Is(err, ErrDeviceEvidenceNotFound) {
			continue
		}
		if err != nil {
			return DeviceEvidenceDetail{}, err
		}
		part.Summary.Device = target
		result.Summary = mergedDeviceSummary(result.Summary, part.Summary)
		result.Truncated = result.Truncated || part.Truncated
		for _, item := range part.Evidence {
			if origin != q.DeviceID {
				item.OriginalDeviceID = origin
			}
			result.Evidence = append(result.Evidence, item)
		}
	}
	if result.Summary.LastSeen.IsZero() {
		return DeviceEvidenceDetail{}, ErrDeviceEvidenceNotFound
	}
	sort.Slice(result.Evidence, func(i, j int) bool {
		a, b := result.Evidence[i], result.Evidence[j]
		if !a.ObservedAt.Equal(b.ObservedAt) {
			return a.ObservedAt.After(b.ObservedAt)
		}
		if a.OriginalDeviceID != b.OriginalDeviceID {
			return a.OriginalDeviceID < b.OriginalDeviceID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Value < b.Value
	})
	if len(result.Evidence) > q.Limit {
		result.Truncated = true
		result.Evidence = result.Evidence[:q.Limit]
	}
	return result, nil
}

func hasOriginalScopeMembership(ctx context.Context, tx *sql.Tx, now time.Time, q DeviceQuery, deviceID string, remaining *int) (bool, error) {
	var legacy bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM devices d
		JOIN device_claim_links l ON l.device_id=d.id
		JOIN identity_claims c ON c.id=l.claim_id
		WHERE d.id=? AND c.scope_id=? AND c.observed_at_ns<=?
		AND (c.valid_until_ns IS NULL OR c.valid_until_ns>=?)
		AND l.valid_from_ns<=? AND (l.valid_until_ns IS NULL OR l.valid_until_ns>=?)
		AND c.expires_at_ns>? AND (d.retired_at_ns IS NULL OR d.retired_at_ns>=?))`,
		deviceID, q.ScopeID, q.AsOf.UnixNano(), q.AsOf.UnixNano(), q.AsOf.UnixNano(), q.AsOf.UnixNano(), now.UnixNano(), q.AsOf.UnixNano()).Scan(&legacy)
	if err != nil || legacy {
		return legacy, err
	}
	batch, err := getBatchDeviceEvidenceDetail(ctx, tx, now,
		DeviceEvidenceDetailQuery{ScopeID: q.ScopeID, DeviceID: deviceID, AsOf: q.AsOf, Limit: 1}, true, remaining)
	if err != nil {
		return false, err
	}
	return len(batch.Evidence) != 0, nil
}

func (s *MixedIdentitySnapshot) listMergedDevicesForScope(ctx context.Context, query DeviceQuery) (DevicePage, error) {
	if s == nil || s.legacy == nil {
		return DevicePage{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceQuery(query, s.legacy.now)
	if err != nil {
		return DevicePage{}, err
	}
	merges, err := loadDeviceMerges(ctx, s.legacy.tx, q.ScopeID)
	if err != nil {
		return DevicePage{}, err
	}
	if len(merges.bySource) == 0 {
		return s.listOriginalDevicesForScope(ctx, q)
	}
	byID := map[string]domain.Device{}
	remaining := mergeProjectionBatchBudget
	for sourceID, targetID := range merges.bySource {
		if targetID <= q.AfterID {
			continue
		}
		present, err := hasOriginalScopeMembership(ctx, s.legacy.tx, s.legacy.now, q, sourceID, &remaining)
		if err != nil {
			return DevicePage{}, err
		}
		if !present {
			continue
		}
		target, err := loadMergeTargetDevice(ctx, s.legacy.tx, targetID, q.AsOf)
		if err != nil {
			return DevicePage{}, err
		}
		byID[targetID] = target
	}
	pageSize := min(MaxQueryLimit, q.Limit+len(merges.bySource)+1)
	cursor := q.AfterID
	nonSource := 0
	for nonSource <= q.Limit {
		page, err := s.listOriginalDevicesForScope(ctx, DeviceQuery{ScopeID: q.ScopeID, AsOf: q.AsOf, AfterID: cursor, Limit: pageSize})
		if err != nil {
			return DevicePage{}, err
		}
		for _, d := range page.Devices {
			if merges.bySource[d.ID] != "" {
				continue
			}
			nonSource++
			byID[d.ID] = d
		}
		if page.NextID == "" || nonSource > q.Limit {
			break
		}
		cursor = page.NextID
	}
	devices := make([]domain.Device, 0, len(byID))
	for _, d := range byID {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	return scopeDevicePage(devices, q.Limit), nil
}
