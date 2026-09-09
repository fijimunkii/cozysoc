package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func (*fakeDeviceStore) LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error) {
	return domain.CoverageSample{}, false, nil
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
	if result.Sources == nil || result.BlindSpots == nil || len(result.Sources) != 0 || len(result.BlindSpots) != 0 {
		t.Fatalf("unconfigured coverage collections = %+v", result)
	}
}

func TestControllerAPIHandlerProjectsCuratedCoverageDetails(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	evidence, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"interface":      "en0",
		"sources": []map[string]any{
			{"method": devicewatch.MethodARPCache, "available": true},
			{"method": devicewatch.MethodNDPCache, "available": false},
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
	store := &fakeCoverageControllerStore{
		fakeDeviceStore: &fakeDeviceStore{},
		ok:              true,
		sample: domain.CoverageSample{
			ID: "coverage.test", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: devicewatch.CapabilityID,
			Status: "partial", StartedAt: now.Add(-time.Minute), EndedAt: now.Add(-time.Minute), SchemaVersion: 1,
			Evidence: evidence, Retention: domain.RetentionShort,
		},
	}
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
	if len(result.BlindSpots) != 3 || result.BlindSpots[2].ID != "no-traffic-monitoring" || result.BlindSpots[2].NextStep == "" {
		t.Fatalf("coverage blind spots = %+v", result.BlindSpots)
	}
}
