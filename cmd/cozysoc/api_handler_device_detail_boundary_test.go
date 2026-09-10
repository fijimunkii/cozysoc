package main

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestControllerAPIHandlerDeviceDetailValidityIsHalfOpen(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	future := now.Add(time.Minute)
	store := &fakeDeviceStore{detail: storage.DeviceEvidenceDetail{
		Summary: storage.DeviceEvidenceSummary{Device: domain.Device{ID: "device.one", CreatedAt: now.Add(-time.Hour)}, FirstSeen: now.Add(-time.Hour), LastSeen: now.Add(-time.Minute)},
		Evidence: []storage.DeviceIdentityEvidence{
			{Kind: domain.ClaimMAC, Value: "02:00:00:00:00:01", ObservedAt: now.Add(-time.Minute), ClaimValidUntil: &now, LinkValidUntil: &future, Authority: domain.LinkInferred, Reason: "fixture", SourceSensorID: "sensor.dw"},
			{Kind: domain.ClaimIPv4, Value: "192.168.1.20", ObservedAt: now.Add(-time.Minute), ClaimValidUntil: &future, LinkValidUntil: &now, Authority: domain.LinkInferred, Reason: "fixture", SourceSensorID: "sensor.dw"},
			{Kind: domain.ClaimIPv6, Value: "2001:db8::20", ObservedAt: now.Add(-time.Minute), ClaimValidUntil: &future, LinkValidUntil: &future, Authority: domain.LinkInferred, Reason: "fixture", SourceSensorID: "sensor.dw"},
		},
	}}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	detail, err := handler.DeviceDetail(context.Background(), api.DeviceDetailParams{DeviceID: "device.one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Evidence) != 3 || detail.Evidence[0].Current || detail.Evidence[1].Current || !detail.Evidence[2].Current {
		t.Fatalf("valid-until boundary is not half-open: %+v", detail.Evidence)
	}
	if detail.Evidence[0].LinkValidUntil == nil || !detail.Evidence[0].LinkValidUntil.Equal(future) || detail.Evidence[1].LinkValidUntil == nil || !detail.Evidence[1].LinkValidUntil.Equal(now) {
		t.Fatalf("link validity was not projected: %+v", detail.Evidence)
	}
}
