package storage

import (
	"context"
	"sort"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// ListDevicesForScope preserves the legacy claim/link presence intervals in the
// caller's fixed snapshot. A history match alone does not establish membership.
func (s *MixedIdentitySnapshot) ListDevicesForScope(ctx context.Context, query DeviceQuery) (DevicePage, error) {
	return s.listMergedDevicesForScope(ctx, query)
}

func (s *MixedIdentitySnapshot) listOriginalDevicesForScope(ctx context.Context, query DeviceQuery) (DevicePage, error) {
	if s == nil || s.legacy == nil {
		return DevicePage{}, ErrEvidenceBatchData
	}
	now := s.legacy.now
	q, err := normalizeDeviceQuery(query, now)
	if err != nil {
		return DevicePage{}, err
	}
	if !batchTimeFits(now) || !batchTimeFits(q.AsOf) {
		return DevicePage{}, ErrEvidenceBatchData
	}
	legacy, err := listLegacyDevicesForScope(ctx, s.legacy.tx, now, q)
	if err != nil {
		return DevicePage{}, err
	}
	through := ""
	if len(legacy) > q.Limit {
		through = legacy[q.Limit].ID
	}
	batch, err := listBatchDeviceCandidates(ctx, s.legacy.tx, now, DeviceEvidenceQuery{ScopeID: q.ScopeID, AsOf: q.AsOf, AfterID: q.AfterID, Limit: q.Limit}, through, true)
	if err != nil {
		return DevicePage{}, err
	}
	byID := make(map[string]domain.Device, len(legacy)+len(batch))
	for _, d := range legacy {
		byID[d.ID] = d
	}
	for _, d := range batch {
		byID[d.Device.ID] = d.Device
	}
	devices := make([]domain.Device, 0, len(byID))
	for _, d := range byID {
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	return scopeDevicePage(devices, q.Limit), nil
}
