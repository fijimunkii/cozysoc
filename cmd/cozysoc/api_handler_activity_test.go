package main

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestControllerAPIHandlerProjectsScopedDeviceActivity(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &fakeDeviceStore{activity: storage.DeviceActivityPage{
		Since: now.Add(-storage.DeviceActivityWindow),
		Items: []storage.DeviceActivityItem{{
			ID: "obs.one", Kind: storage.DeviceActivityAddressChanged, At: now.Add(-time.Minute), DeviceID: "device.one", UserLabel: "TV",
			AddressFamily: "ipv4", Address: "192.168.1.20", PreviousAddress: "192.168.1.10", HardwareAddress: "02:00:00:00:00:01",
			Source: storage.DeviceActivitySource{ObservationID: "obs.one", SensorID: "sensor.dw", Kind: "device-neighbor-seen", SourceStream: "device-watch-neighbors", IngestedAt: now.Add(-time.Minute), Attribution: "device-watch:arp-cache"},
		}},
	}}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	activity, err := handler.DeviceActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !activity.Configured || activity.ScopeID != "scope.home" || len(activity.Items) != 1 || activity.Items[0].Kind != "address-changed" {
		t.Fatalf("unexpected activity: %+v", activity)
	}
	if store.activityQuery.ScopeID != "scope.home" || !store.activityQuery.AsOf.Equal(now) || store.activityQuery.Limit != storage.MaxDeviceActivityItems {
		t.Fatalf("activity query escaped current scope: %+v", store.activityQuery)
	}
	if activity.Items[0].Source.Attribution != "device-watch:arp-cache" || activity.Items[0].PreviousAddress != "192.168.1.10" {
		t.Fatalf("activity projection lost evidence: %+v", activity.Items[0])
	}
}

func TestControllerAPIHandlerActivityDisabledIsEmpty(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), &fakeDeviceStore{}, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	activity, err := handler.DeviceActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if activity.Configured || activity.ScopeID != "" || len(activity.Items) != 0 || !activity.AsOf.Equal(now) || !activity.Since.Equal(now.Add(-storage.DeviceActivityWindow)) {
		t.Fatalf("unexpected disabled activity: %+v", activity)
	}
}

var _ api.DeviceActivityList
