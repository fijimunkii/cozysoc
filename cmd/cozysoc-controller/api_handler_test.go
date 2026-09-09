package main

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type fakePresenceReader struct {
	page storage.DeviceEvidencePage
	err  error
}

func (f fakePresenceReader) ListDeviceEvidence(context.Context, storage.DeviceEvidenceQuery) (storage.DeviceEvidencePage, error) {
	return f.page, f.err
}

func TestControllerAPIHandlerListsConfiguredDevicePresence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	reader := fakePresenceReader{page: storage.DeviceEvidencePage{
		Devices: []storage.DeviceEvidenceSummary{
			{
				Device:    domain.Device{ID: "device.one", UserLabel: "Camera", CreatedAt: now.Add(-24 * time.Hour)},
				FirstSeen: now.Add(-24 * time.Hour),
				LastSeen:  now.Add(-time.Minute),
			},
			{
				Device:    domain.Device{ID: "device.two", CreatedAt: now.Add(-48 * time.Hour)},
				FirstSeen: now.Add(-48 * time.Hour),
				LastSeen:  now.Add(-10 * time.Minute),
			},
		},
		NextID: "device.three",
	}}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), reader, "scope.home", true)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	list, err := handler.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !list.Configured || list.ScopeID != "scope.home" || !list.Truncated || len(list.Devices) != 2 {
		t.Fatalf("unexpected device list: %+v", list)
	}
	if list.Devices[0].UserLabel != "Camera" || list.Devices[0].State != "visible" {
		t.Fatalf("unexpected visible device: %+v", list.Devices[0])
	}
	if list.Devices[1].State != "uncertain" {
		t.Fatalf("stale device was not uncertain: %+v", list.Devices[1])
	}
}

func TestControllerAPIHandlerReturnsDisabledDeviceWatchState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	list, err := handler.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list.Configured || list.ScopeID != "" || list.Truncated || len(list.Devices) != 0 || !list.AsOf.Equal(now) {
		t.Fatalf("unexpected disabled device list: %+v", list)
	}
}
