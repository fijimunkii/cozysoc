package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func (*fakeDeviceStore) LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error) {
	return domain.CoverageSample{}, false, nil
}

func (*fakeDeviceWatchAPIControl) OperationalHealth(_ context.Context, at time.Time) (devicewatch.OperationalHealth, error) {
	return healthyOperational(at), nil
}

type fakeCoverageControllerStore struct {
	*fakeDeviceStore
	sample domain.CoverageSample
	ok     bool
	err    error
}

func (f *fakeCoverageControllerStore) LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error) {
	return f.sample, f.ok, f.err
}

type fakeOperationalCoverageControl struct {
	*fakeDeviceWatchAPIControl
	operational devicewatch.OperationalHealth
}

func (f *fakeOperationalCoverageControl) OperationalHealth(context.Context, time.Time) (devicewatch.OperationalHealth, error) {
	return f.operational, nil
}

func TestControllerAPIHandlerReturnsUnconfiguredCoverageState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), &fakeDeviceStore{}, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	result, err := handler.DeviceWatchCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Configured || result.State != "unconfigured" || result.ScopeID != "" || !result.AsOf.Equal(now) {
		t.Fatalf("unconfigured coverage = %+v", result)
	}
	if result.Sources == nil || result.BlindSpots == nil || len(result.Sources) != 0 || len(result.BlindSpots) != 0 || result.Operational != nil {
		t.Fatalf("unconfigured coverage collections = %+v", result)
	}
}

func TestControllerAPIHandlerProjectsCuratedCoverageDetails(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := coverageControllerStore(t, now, true, false)
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	result, err := handler.DeviceWatchCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.ScopeID != "scope.home" || result.State != "degraded" || result.Reason != "source-partial" {
		t.Fatalf("coverage result = %+v", result)
	}
	if result.SensorID != "sensor.dw.test" || result.InterfaceName != "en0" || result.EvidenceAt == nil || result.FreshUntil == nil || result.NeighborsInScope == nil || *result.NeighborsInScope != 1 {
		t.Fatalf("coverage evidence metadata = %+v", result)
	}
	if len(result.Sources) != 2 || result.Sources[0].State != "current" || result.Sources[1].State != "unavailable" || result.Sources[1].NextStep == "" {
		t.Fatalf("coverage sources = %+v", result.Sources)
	}
	if result.Operational == nil || result.Operational.Sensor.State != "current" || result.Operational.Pipeline.State != "current" || result.Operational.Database.State != "current" {
		t.Fatalf("coverage operational health = %+v", result.Operational)
	}
	if result.Operational.Database.QuotaState != "current" || result.Operational.Database.FilesystemState != "current" || !result.Operational.Database.FilesystemSupported || result.Operational.Database.FilesystemAvailableBytes <= 0 {
		t.Fatalf("coverage storage detail = %+v", result.Operational.Database)
	}
	if len(result.BlindSpots) != 3 || result.BlindSpots[2].ID != "no-traffic-monitoring" || result.BlindSpots[2].NextStep == "" {
		t.Fatalf("coverage blind spots = %+v", result.BlindSpots)
	}
}

func TestControllerAPIHandlerMakesSensorDisconnectionPrimary(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := coverageControllerStore(t, now, true, true)
	operational := healthyOperational(now)
	operational.Sensor = devicewatch.SensorHealth{
		State:    devicewatch.OperationalDisconnected,
		Running:  false,
		NextStep: "Restart Device Watch.",
	}
	control := &fakeOperationalCoverageControl{
		fakeDeviceWatchAPIControl: &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true},
		operational:               operational,
	}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	result, err := handler.DeviceWatchCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "disconnected" || result.Reason != "sensor-disconnected" || result.NextStep != "Restart Device Watch." {
		t.Fatalf("disconnected coverage result = %+v", result)
	}
	if result.Operational == nil || result.Operational.Sensor.Running || result.Operational.Sensor.State != "disconnected" {
		t.Fatalf("disconnected sensor projection = %+v", result.Operational)
	}
}

func TestControllerAPIHandlerDiagnosesFilesystemFullWriteFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	storeFixture := coverageControllerStore(t, now, true, true)
	operational := healthyOperational(now)
	operational.Pipeline = devicewatch.PipelineHealth{
		State:        devicewatch.OperationalDegraded,
		Reason:       "sqlite-full",
		FailureClass: storage.IngestionFailureSQLiteFull,
		Capacity:     256,
		Failed:       1,
		NextStep:     "Wait for write recovery.",
	}
	operational.Database = devicewatch.DatabaseHealth{
		State:                     devicewatch.OperationalDegraded,
		Reason:                    "filesystem-full",
		QuotaState:                storage.HealthCurrent,
		FilesystemState:           storage.FilesystemCapacityFull,
		FilesystemSupported:       true,
		DatabaseBytes:             4096,
		UsedBytes:                 4096,
		MaxBytes:                  1 << 30,
		FilesystemTotalBytes:      1 << 30,
		FilesystemAvailableBytes:  0,
		FilesystemPressureAtBytes: 128 << 20,
		NextStep:                  "Free disk space.",
	}
	control := &fakeOperationalCoverageControl{
		fakeDeviceWatchAPIControl: &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true},
		operational:               operational,
	}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), storeFixture, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	result, err := handler.DeviceWatchCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "degraded" || result.Reason != "storage-filesystem-full" || result.NextStep != "Free disk space." {
		t.Fatalf("filesystem full coverage result = %+v", result)
	}
	if result.Operational == nil || result.Operational.Pipeline.FailureClass != storage.IngestionFailureSQLiteFull || result.Operational.Database.FilesystemState != "full" || result.Operational.Database.QuotaState != "current" {
		t.Fatalf("filesystem full operational projection = %+v", result.Operational)
	}
}

func coverageControllerStore(t *testing.T, now time.Time, arpAvailable, ndpAvailable bool) *fakeCoverageControllerStore {
	t.Helper()
	evidence, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"interface":      "en0",
		"sources": []map[string]any{
			{"method": devicewatch.MethodARPCache, "available": arpAvailable},
			{"method": devicewatch.MethodNDPCache, "available": ndpAvailable},
		},
		"neighbors_in_scope":            1,
		"observations_inserted":         1,
		"observations_deduplicated":     0,
		"whole_network_traffic_visible": false,
		"limitations": []string{
			"passive neighbor caches include only peers the host has recently resolved on the local link",
			"client isolation, other VLANs, and devices behind other observation points may be absent",
			"a successful neighbor snapshot does not provide whole-network traffic visibility",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeCoverageControllerStore{
		fakeDeviceStore: &fakeDeviceStore{},
		ok:              true,
		sample: domain.CoverageSample{
			ID: "coverage.test", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: devicewatch.CapabilityID,
			Status: "partial", StartedAt: now.Add(-time.Minute), EndedAt: now.Add(-time.Minute), SchemaVersion: 1,
			Evidence: evidence, Retention: domain.RetentionShort,
		},
	}
}

func healthyOperational(now time.Time) devicewatch.OperationalHealth {
	return devicewatch.OperationalHealth{
		Sensor: devicewatch.SensorHealth{
			State:            devicewatch.OperationalCurrent,
			Running:          true,
			LastAttemptAt:    now.Add(-time.Minute),
			LastSuccessfulAt: now.Add(-time.Minute),
		},
		Pipeline: devicewatch.PipelineHealth{State: devicewatch.OperationalCurrent, Capacity: 256},
		Database: devicewatch.DatabaseHealth{
			State:                     devicewatch.OperationalCurrent,
			QuotaState:                storage.HealthCurrent,
			FilesystemState:           storage.FilesystemCapacityCurrent,
			FilesystemSupported:       true,
			DatabaseBytes:             1024,
			UsedBytes:                 1024,
			MaxBytes:                  1 << 30,
			FilesystemTotalBytes:      100 << 30,
			FilesystemAvailableBytes:  50 << 30,
			FilesystemPressureAtBytes: 128 << 20,
		},
	}
}
