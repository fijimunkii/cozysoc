package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type coverageTestHandler struct {
	mutationTestHandler
	result api.DeviceWatchCoverage
	err    error
}

func (h *coverageTestHandler) DeviceWatchCoverage(context.Context) (api.DeviceWatchCoverage, error) {
	return h.result, h.err
}

func TestDeviceWatchCoverageRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	handler := &coverageTestHandler{result: api.DeviceWatchCoverage{
		Configured: true,
		ScopeID:    "scope.home",
		AsOf:       now,
		State:      "active-limited",
		Reason:     "fresh-limited",
		Sources: []api.DeviceWatchCoverageSource{
			{ID: "arp-cache", AddressFamily: "ipv4", State: "current", Reported: true, AvailableAtLastSample: true},
			{ID: "ndp-cache", AddressFamily: "ipv6", State: "unavailable", Reported: true, NextStep: "Check NDP access."},
		},
		BlindSpots: []api.DeviceWatchCoverageBlindSpot{
			{ID: "no-traffic-monitoring", Summary: "No traffic visibility", Detail: "Neighbor evidence is not traffic evidence.", NextStep: "Use Traffic Watch."},
		},
		NextStep: "Restore NDP if IPv6 visibility matters.",
	}}
	server := startMutationTestServer(t, handler)
	raw, err := NewClient(server.stateDir).Call(context.Background(), api.MethodDeviceWatchCoverage)
	if err != nil {
		t.Fatal(err)
	}
	var result api.DeviceWatchCoverage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.ScopeID != "scope.home" || result.State != "active-limited" || len(result.Sources) != 2 || len(result.BlindSpots) != 1 {
		t.Fatalf("coverage round trip = %+v", result)
	}
}

func TestDeviceWatchCoverageRejectsParams(t *testing.T) {
	server := startMutationTestServer(t, &coverageTestHandler{})
	_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodDeviceWatchCoverage, map[string]any{"scope_id": "scope.other"})
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("coverage params error = %v", err)
	}
}

func TestDeviceWatchCoverageMapsHandlerFailureSafely(t *testing.T) {
	server := startMutationTestServer(t, &coverageTestHandler{err: errors.New("secret storage detail")})
	_, err := NewClient(server.stateDir).Call(context.Background(), api.MethodDeviceWatchCoverage)
	if err == nil || !strings.Contains(err.Error(), "internal_error") || strings.Contains(err.Error(), "secret storage detail") {
		t.Fatalf("coverage handler error = %v", err)
	}
}
