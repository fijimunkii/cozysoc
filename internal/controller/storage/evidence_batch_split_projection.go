package storage

import (
	"context"
	"errors"
	"sort"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func (s *MixedIdentitySnapshot) getCorrectedDeviceEvidenceDetail(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceEvidenceDetail, error) {
	if s == nil || s.legacy == nil {
		return DeviceEvidenceDetail{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceDetailQuery(query, s.legacy.now)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	splits, err := loadDeviceSplits(ctx, s.legacy.tx, q.ScopeID)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	if len(splits.bySource[q.DeviceID]) == 0 && len(splits.byTarget[q.DeviceID]) == 0 {
		return s.getMergedDeviceEvidenceDetail(ctx, q)
	}
	device, err := loadMergeTargetDevice(ctx, s.legacy.tx, q.DeviceID, q.AsOf)
	if err != nil {
		return DeviceEvidenceDetail{}, err
	}
	result := deviceEvidenceDetailRows{Summary: DeviceEvidenceSummary{Device: device}}
	remaining := splitProjectionBatchBudget
	appendRow := func(row deviceEvidenceRow, origin string) {
		if origin != q.DeviceID {
			row.Evidence.OriginalDeviceID = origin
		}
		result.Evidence = append(result.Evidence, row)
		at := row.Evidence.ObservedAt
		if result.Summary.FirstSeen.IsZero() || at.Before(result.Summary.FirstSeen) {
			result.Summary.FirstSeen = at
		}
		if at.After(result.Summary.LastSeen) {
			result.Summary.LastSeen = at
		}
	}
	// The device's own current page may contain links moved elsewhere. Scan
	// enough additional rows to replace every displaced link before paging.
	rawQuery := q
	rawQuery.Limit = q.Limit + len(splits.byLink)
	part, err := s.getOriginalDeviceEvidenceRowsLimited(ctx, rawQuery, &remaining)
	if err != nil && !errors.Is(err, ErrDeviceEvidenceNotFound) {
		return DeviceEvidenceDetail{}, err
	}
	ownEvidence := false
	if err == nil {
		for _, row := range part.Evidence {
			if split, moved := splits.byLink[row.LinkID]; moved {
				if split.SourceDeviceID != q.DeviceID {
					return DeviceEvidenceDetail{}, ErrEvidenceBatchData
				}
				continue
			}
			appendRow(row, q.DeviceID)
			ownEvidence = true
		}
	}
	// A split observation can age far beyond the source's first detail page.
	// Read its original time window directly instead of assuming it remains
	// among that source's newest 100 links.
	for _, correction := range splits.byTarget[q.DeviceID] {
		routes := splits.linksByObservation[correction.ObservationID]
		if len(routes) == 0 {
			return DeviceEvidenceDetail{}, ErrEvidenceBatchData
		}
		at := routes[0].ObservedAt
		if at.After(q.AsOf) {
			continue
		}
		selected := map[string]bool{}
		for _, route := range routes {
			if !route.ObservedAt.Equal(at) {
				return DeviceEvidenceDetail{}, ErrEvidenceBatchData
			}
			selected[route.LinkID] = true
		}
		window := DeviceEvidenceDetailQuery{ScopeID: q.ScopeID, DeviceID: correction.SourceDeviceID, AsOf: at, Limit: MaxDeviceDetailEvidence}
		part, err := s.getOriginalDeviceEvidenceRowsLimited(ctx, window, &remaining)
		if errors.Is(err, ErrDeviceEvidenceNotFound) {
			continue
		}
		if err != nil {
			return DeviceEvidenceDetail{}, err
		}
		if len(part.Evidence) > MaxDeviceDetailEvidence && part.Evidence[MaxDeviceDetailEvidence].Evidence.ObservedAt.Equal(at) {
			return DeviceEvidenceDetail{}, ErrEvidenceBatchQueryLimit
		}
		for _, row := range part.Evidence {
			if !selected[row.LinkID] {
				continue
			}
			if split, ok := splits.byLink[row.LinkID]; !ok || split != correction {
				return DeviceEvidenceDetail{}, ErrEvidenceBatchData
			}
			appendRow(row, correction.SourceDeviceID)
		}
	}
	if len(result.Evidence) == 0 {
		return DeviceEvidenceDetail{}, ErrDeviceEvidenceNotFound
	}
	if ownEvidence {
		first, err := correctedSourceFirstSeen(ctx, s.legacy.tx, q.ScopeID, q.DeviceID, device.CreatedAt, q.AsOf, s.legacy.now, splits)
		if err != nil {
			return DeviceEvidenceDetail{}, err
		}
		if first.Before(result.Summary.FirstSeen) {
			result.Summary.FirstSeen = first
		}
	}
	sortDeviceEvidenceRows(result.Evidence)
	// The public response has no link IDs. Sorting above retains the original
	// claim/link order before stripping that internal correction key.
	if len(result.Evidence) > q.Limit {
		result.Evidence = result.Evidence[:q.Limit+1]
	}
	return result.page(q.Limit), nil
}

func correctedSummaryFromDetail(detail DeviceEvidenceDetail) DeviceEvidenceSummary {
	return detail.Summary
}

func sortCorrectedSummaries(items []DeviceEvidenceSummary) {
	sort.Slice(items, func(i, j int) bool { return items[i].Device.ID < items[j].Device.ID })
}

func (s *MixedIdentitySnapshot) listCorrectedDeviceEvidence(ctx context.Context, query DeviceEvidenceQuery) (DeviceEvidencePage, error) {
	if s == nil || s.legacy == nil {
		return DeviceEvidencePage{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceEvidenceQuery(query, s.legacy.now)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	splits, err := loadDeviceSplits(ctx, s.legacy.tx, q.ScopeID)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	if len(splits.byObservation) == 0 {
		return s.listMergedDeviceEvidence(ctx, q)
	}
	byID := map[string]DeviceEvidenceSummary{}
	addCorrected := func(id string) error {
		if id <= q.AfterID {
			return nil
		}
		detail, err := s.getCorrectedDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: q.ScopeID, DeviceID: id, AsOf: q.AsOf, Limit: 1})
		if errors.Is(err, ErrDeviceEvidenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		byID[id] = correctedSummaryFromDetail(detail)
		return nil
	}
	for targetID := range splits.byTarget {
		if err := addCorrected(targetID); err != nil {
			return DeviceEvidencePage{}, err
		}
	}
	cursor := q.AfterID
	pageSize := min(MaxQueryLimit, q.Limit+len(splits.bySource)+1)
	for len(byID) <= q.Limit {
		page, err := s.listMergedDeviceEvidence(ctx, DeviceEvidenceQuery{ScopeID: q.ScopeID, AsOf: q.AsOf, AfterID: cursor, Limit: pageSize})
		if err != nil {
			return DeviceEvidencePage{}, err
		}
		for _, summary := range page.Devices {
			id := summary.Device.ID
			if len(splits.bySource[id]) != 0 || len(splits.byTarget[id]) != 0 {
				if err := addCorrected(id); err != nil {
					return DeviceEvidencePage{}, err
				}
			} else {
				byID[id] = summary
			}
		}
		if page.NextID == "" {
			break
		}
		if page.NextID <= cursor {
			return DeviceEvidencePage{}, ErrEvidenceBatchData
		}
		cursor = page.NextID
	}
	result := make([]DeviceEvidenceSummary, 0, len(byID))
	for _, summary := range byID {
		result = append(result, summary)
	}
	sortCorrectedSummaries(result)
	return deviceEvidencePage(result, q.Limit), nil
}

func (s *MixedIdentitySnapshot) listCorrectedDevicesForScope(ctx context.Context, query DeviceQuery) (DevicePage, error) {
	if s == nil || s.legacy == nil {
		return DevicePage{}, ErrEvidenceBatchData
	}
	q, err := normalizeDeviceQuery(query, s.legacy.now)
	if err != nil {
		return DevicePage{}, err
	}
	splits, err := loadDeviceSplits(ctx, s.legacy.tx, q.ScopeID)
	if err != nil {
		return DevicePage{}, err
	}
	if len(splits.byObservation) == 0 {
		return s.listMergedDevicesForScope(ctx, q)
	}
	byID := map[string]domain.Device{}
	addCorrected := func(id string) error {
		if id <= q.AfterID {
			return nil
		}
		detail, err := s.getCorrectedDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: q.ScopeID, DeviceID: id, AsOf: q.AsOf})
		if errors.Is(err, ErrDeviceEvidenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, item := range detail.Evidence {
			if (item.ClaimValidUntil == nil || !item.ClaimValidUntil.Before(q.AsOf)) && (item.LinkValidUntil == nil || !item.LinkValidUntil.Before(q.AsOf)) {
				byID[id] = detail.Summary.Device
				return nil
			}
		}
		if detail.Truncated {
			return ErrEvidenceBatchQueryLimit
		}
		return nil
	}
	for targetID := range splits.byTarget {
		if err := addCorrected(targetID); err != nil {
			return DevicePage{}, err
		}
	}
	cursor := q.AfterID
	pageSize := min(MaxQueryLimit, q.Limit+len(splits.bySource)+1)
	for len(byID) <= q.Limit {
		page, err := s.listMergedDevicesForScope(ctx, DeviceQuery{ScopeID: q.ScopeID, AsOf: q.AsOf, AfterID: cursor, Limit: pageSize})
		if err != nil {
			return DevicePage{}, err
		}
		for _, device := range page.Devices {
			if len(splits.bySource[device.ID]) != 0 || len(splits.byTarget[device.ID]) != 0 {
				if err := addCorrected(device.ID); err != nil {
					return DevicePage{}, err
				}
			} else {
				byID[device.ID] = device
			}
		}
		if page.NextID == "" {
			break
		}
		if page.NextID <= cursor {
			return DevicePage{}, ErrEvidenceBatchData
		}
		cursor = page.NextID
	}
	result := make([]domain.Device, 0, len(byID))
	for _, device := range byID {
		result = append(result, device)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return scopeDevicePage(result, q.Limit), nil
}
