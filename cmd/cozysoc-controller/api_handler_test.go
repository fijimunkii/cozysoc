package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type fakeDeviceStore struct {
	page       storage.DeviceEvidencePage
	err        error
	setScope   string
	setDevice  string
	setLabel   string
	setChanged bool
	setErr     error
}

func (f *fakeDeviceStore) ListDeviceEvidence(context.Context, storage.DeviceEvidenceQuery) (storage.DeviceEvidencePage, error) {
	return f.page, f.err
}

func (f *fakeDeviceStore) SetDeviceLabel(_ context.Context, scopeID, deviceID, label string) (bool, error) {
	f.setScope = scopeID
	f.setDevice = deviceID
	f.setLabel = label
	return f.setChanged, f.setErr
}

func TestControllerAPIHandlerListsConfiguredDevicePresence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &fakeDeviceStore{page: storage.DeviceEvidencePage{
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
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, "scope.home", true)
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

func TestControllerAPIHandlerLabelsOnlyConfiguredScope(t *testing.T) {
	store := &fakeDeviceStore{setChanged: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, "scope.home", true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.LabelDevice(context.Background(), api.DeviceLabelParams{DeviceID: "device.one", Label: stringPtr("Living Room TV")})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.DeviceID != "device.one" || result.UserLabel != "Living Room TV" {
		t.Fatalf("unexpected label result: %+v", result)
	}
	if store.setScope != "scope.home" || store.setDevice != "device.one" || store.setLabel != "Living Room TV" {
		t.Fatalf("mutation escaped configured scope: %+v", store)
	}
}

func TestControllerAPIHandlerRejectsInvalidAndUnavailableLabelTargets(t *testing.T) {
	store := &fakeDeviceStore{}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, "scope.home", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []api.DeviceLabelParams{
		{DeviceID: "device.one"},
		{DeviceID: "../device", Label: stringPtr("TV")},
		{DeviceID: "device.one", Label: stringPtr(" TV")},
	} {
		if _, err := handler.LabelDevice(context.Background(), params); !errors.Is(err, localapi.ErrInvalidMutation) {
			t.Fatalf("invalid params %+v error = %v", params, err)
		}
	}

	store.setErr = storage.ErrDeviceNotInScope
	if _, err := handler.LabelDevice(context.Background(), api.DeviceLabelParams{DeviceID: "device.one", Label: stringPtr("TV")}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatalf("out-of-scope error = %v", err)
	}

	disabled, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.LabelDevice(context.Background(), api.DeviceLabelParams{DeviceID: "device.one", Label: stringPtr("TV")}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatalf("disabled mutation error = %v", err)
	}
}

func stringPtr(value string) *string {
	return &value
}
