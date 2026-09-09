package localapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestDeviceWatchSharedCoverageContractRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	handler := &coverageTestHandler{result: api.DeviceWatchCoverage{
		Configured: true,
		AsOf:       now,
		State:      "active-limited",
		Coverage: &api.CoverageReport{
			CapabilityID: "device-watch",
			Configured:   true,
			State:        "active-limited",
			Reason:       "fresh-limited",
			ObservationPoints: []api.CoverageObservationPoint{{
				ID:       "device-watch.local-neighbor-cache",
				Kind:     "host-neighbor-cache",
				SensorID: "sensor.dw.test",
				State:    "active-limited",
				Reason:   "fresh-limited",
				Scope: api.CoverageScope{
					Configured: []api.CoverageDimension{{Kind: "network", Value: "scope.home"}},
					Verified:   []api.CoverageDimension{{Kind: "network", Value: "scope.home"}},
				},
				Sources:    []api.CoverageSource{{ID: "arp-cache", Kind: "neighbor-cache", State: "current", Expected: true, Observed: true}},
				Directions: []string{},
				Window: api.CoverageEvidenceWindow{
					HasEvidence: true,
					StartedAt:   &now,
					EndedAt:     &now,
					FreshUntil:  pointerTime(now.Add(3 * time.Minute)),
				},
				Cadence: api.CoverageCadence{Mode: "periodic", IntervalMS: 60_000},
				Gaps: []api.CoverageGap{{
					ID:         "no-traffic-monitoring",
					Kind:       "direction-not-observed",
					Summary:    "No traffic visibility",
					Detail:     "Neighbor evidence is not traffic evidence.",
					NextStep:   "Use a validated packet observation point.",
					Dimensions: []api.CoverageDimension{},
					Directions: []string{"ingress", "egress", "east-west"},
				}},
				NextStep: "Use Traffic Watch for packet visibility.",
			}},
			NextStep: "Use Traffic Watch for packet visibility.",
		},
		Sources:    []api.DeviceWatchCoverageSource{},
		BlindSpots: []api.DeviceWatchCoverageBlindSpot{},
		NextStep:   "Use Traffic Watch for packet visibility.",
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
	if result.Coverage == nil || result.Coverage.CapabilityID != "device-watch" || len(result.Coverage.ObservationPoints) != 1 {
		t.Fatalf("shared coverage round trip = %+v", result.Coverage)
	}
	point := result.Coverage.ObservationPoints[0]
	if point.Kind != "host-neighbor-cache" || len(point.Directions) != 0 || point.Cadence.IntervalMS != 60_000 || len(point.Gaps) != 1 || len(point.Gaps[0].Directions) != 3 {
		t.Fatalf("shared observation point round trip = %+v", point)
	}
}

func pointerTime(value time.Time) *time.Time {
	return &value
}
