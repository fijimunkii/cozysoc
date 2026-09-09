package devicewatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type fakeCoverageReader struct {
	sample domain.CoverageSample
	ok     bool
	err    error
}

func (f fakeCoverageReader) LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error) {
	return f.sample, f.ok, f.err
}

func TestCoverageVerificationSignalStates(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	falseTrafficClaim := coverageFixture(now.Add(-time.Minute), "partial")
	var falseTrafficEvidence map[string]any
	if err := json.Unmarshal(falseTrafficClaim.Evidence, &falseTrafficEvidence); err != nil {
		t.Fatal(err)
	}
	falseTrafficEvidence["whole_network_traffic_visible"] = true
	encoded, err := json.Marshal(falseTrafficEvidence)
	if err != nil {
		t.Fatal(err)
	}
	falseTrafficClaim.Evidence = encoded

	tests := []struct {
		name   string
		reader fakeCoverageReader
		want   capability.VerificationSignalStatus
	}{
		{name: "missing", reader: fakeCoverageReader{}, want: capability.SignalMissing},
		{name: "fresh partial", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Minute), "partial")}, want: capability.SignalFresh},
		{name: "fresh one-source gap", reader: fakeCoverageReader{ok: true, sample: coverageFixtureWithSources(now.Add(-time.Minute), "partial", true, false)}, want: capability.SignalFailed},
		{name: "fresh unavailable", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Minute), "unavailable")}, want: capability.SignalFailed},
		{name: "stale historical replay", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Hour), "partial")}, want: capability.SignalStale},
		{name: "future clock skew", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(2*time.Minute), "partial")}, want: capability.SignalFailed},
		{name: "unknown status fails closed", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Minute), "mystery")}, want: capability.SignalFailed},
		{name: "false traffic claim fails closed", reader: fakeCoverageReader{ok: true, sample: falseTrafficClaim}, want: capability.SignalFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signal, err := coverageVerificationSignal(context.Background(), test.reader, "scope.home", now)
			if err != nil {
				t.Fatal(err)
			}
			if signal.ID != "observation-freshness" || signal.Status != test.want {
				t.Fatalf("coverage signal = %+v, want status %s", signal, test.want)
			}
		})
	}
}

func TestFreshCoverageDoesNotRequireObservedNeighbors(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sample := coverageFixture(now.Add(-time.Minute), "partial")
	signal, err := coverageVerificationSignal(context.Background(), fakeCoverageReader{sample: sample, ok: true}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	if signal.Status != capability.SignalFresh {
		t.Fatalf("quiet but current coverage = %+v", signal)
	}
}

func coverageFixture(endedAt time.Time, status string) domain.CoverageSample {
	arpAvailable := true
	ndpAvailable := true
	if status == "unavailable" {
		arpAvailable = false
		ndpAvailable = false
	}
	return coverageFixtureWithSources(endedAt, status, arpAvailable, ndpAvailable)
}

func coverageFixtureWithSources(endedAt time.Time, status string, arpAvailable, ndpAvailable bool) domain.CoverageSample {
	evidence, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"interface":      "en0",
		"sources": []map[string]any{
			{"method": MethodARPCache, "available": arpAvailable},
			{"method": MethodNDPCache, "available": ndpAvailable},
		},
		"neighbors_in_scope":            0,
		"observations_inserted":         0,
		"observations_deduplicated":     0,
		"whole_network_traffic_visible": false,
		"limitations": []string{
			"passive neighbor caches include only peers the host has recently resolved on the local link",
			"client isolation, other VLANs, and devices behind other observation points may be absent",
			"a successful neighbor snapshot does not provide whole-network traffic visibility",
		},
	})
	return domain.CoverageSample{
		ID: "coverage.test", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: CapabilityID,
		Status: status, StartedAt: endedAt, EndedAt: endedAt, SchemaVersion: 1,
		Evidence: evidence, Retention: domain.RetentionShort,
	}
}
