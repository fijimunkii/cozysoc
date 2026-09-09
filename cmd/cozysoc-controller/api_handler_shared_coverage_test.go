package main

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
)

func TestControllerAPIHandlerReturnsSharedUnconfiguredCoverage(t *testing.T) {
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
	if result.Coverage == nil || result.Coverage.CapabilityID != devicewatch.CapabilityID || result.Coverage.Configured || result.Coverage.State != "unconfigured" || len(result.Coverage.ObservationPoints) != 0 {
		t.Fatalf("shared unconfigured coverage = %+v", result.Coverage)
	}
	if result.State != result.Coverage.State || result.Reason != result.Coverage.Reason || result.NextStep != result.Coverage.NextStep {
		t.Fatalf("legacy/shared state drift = top=%+v shared=%+v", result, result.Coverage)
	}
}

func TestControllerAPIHandlerProjectsSharedDeviceWatchObservationPoint(t *testing.T) {
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
	if result.Coverage == nil || len(result.Coverage.ObservationPoints) != 1 {
		t.Fatalf("shared coverage = %+v", result.Coverage)
	}
	shared := result.Coverage
	point := shared.ObservationPoints[0]
	if shared.State != "degraded" || shared.Reason != "source-partial" || point.Kind != "host-neighbor-cache" || point.State != shared.State || point.Reason != shared.Reason {
		t.Fatalf("shared observation point = %+v", point)
	}
	if result.State != shared.State || result.Reason != shared.Reason || result.NextStep != shared.NextStep {
		t.Fatalf("legacy/shared aggregate drift = top=%+v shared=%+v", result, shared)
	}
	if !hasAPICoverageDimension(point.Scope.Configured, "network", "scope.home") || !hasAPICoverageDimension(point.Scope.Configured, "interface", "en0") {
		t.Fatalf("shared configured scope = %+v", point.Scope)
	}
	if !hasAPICoverageDimension(point.Scope.Verified, "address-family", "ipv4") || hasAPICoverageDimension(point.Scope.Verified, "address-family", "ipv6") {
		t.Fatalf("shared verified scope = %+v", point.Scope.Verified)
	}
	if !hasAPICoverageDimension(point.Scope.ExpectedUnverified, "address-family", "ipv6") {
		t.Fatalf("shared expected-unverified scope = %+v", point.Scope.ExpectedUnverified)
	}
	if len(point.Directions) != 0 || point.Window.EndedAt == nil || point.Window.FreshUntil == nil || point.Cadence.Mode != "periodic" || point.Cadence.IntervalMS != 60_000 {
		t.Fatalf("shared point window/cadence/directions = %+v", point)
	}
	if len(point.Gaps) != 3 || point.Gaps[2].ID != "no-traffic-monitoring" || len(point.Gaps[2].Directions) != 3 {
		t.Fatalf("shared gaps = %+v", point.Gaps)
	}
}

func hasAPICoverageDimension(dimensions []apiCoverageDimensionLike, kind, value string) bool {
	for _, dimension := range dimensions {
		if dimension.coverageKind() == kind && dimension.coverageValue() == value {
			return true
		}
	}
	return false
}

type apiCoverageDimensionLike interface {
	coverageKind() string
	coverageValue() string
}
