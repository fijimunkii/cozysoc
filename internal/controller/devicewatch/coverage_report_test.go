package devicewatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestCurrentCoverageReportsMissingEvidenceAndBlindSpots(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	report, err := CurrentCoverage(context.Background(), fakeCoverageReader{}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != CoverageUnverified || report.Reason != "no-evidence" || report.HasEvidence {
		t.Fatalf("missing coverage report = %+v", report)
	}
	if len(report.Sources) != 2 || report.Sources[0].State != CoverageSourceMissing || report.Sources[1].State != CoverageSourceMissing {
		t.Fatalf("missing source details = %+v", report.Sources)
	}
	if len(report.BlindSpots) != 3 || report.BlindSpots[2].ID != "no-traffic-monitoring" {
		t.Fatalf("missing blind spots = %+v", report.BlindSpots)
	}
}

func TestCurrentCoverageReportsCurrentAndUnavailableSourcesIndependently(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sample := coverageDetailFixture(t, now.Add(-time.Minute), "partial", true, false, 2)
	report, err := CurrentCoverage(context.Background(), fakeCoverageReader{sample: sample, ok: true}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != CoverageActiveLimited || report.Reason != "fresh-limited" || !report.HasEvidence || report.NeighborsInScope != 2 {
		t.Fatalf("partial coverage report = %+v", report)
	}
	if report.InterfaceName != "en0" || report.SensorID != "sensor.dw.test" || report.EvidenceAt.IsZero() || report.FreshUntil.IsZero() {
		t.Fatalf("coverage evidence metadata = %+v", report)
	}
	if len(report.Sources) != 2 || report.Sources[0].ID != string(MethodARPCache) || report.Sources[0].State != CoverageSourceCurrent {
		t.Fatalf("ARP source detail = %+v", report.Sources)
	}
	if report.Sources[1].ID != string(MethodNDPCache) || report.Sources[1].State != CoverageSourceUnavailable || report.Sources[1].NextStep == "" {
		t.Fatalf("NDP source detail = %+v", report.Sources)
	}
}

func TestCurrentCoverageReportsUnavailableStaleAndInvalidStates(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	tests := []struct {
		name   string
		sample domain.CoverageSample
		want   CoverageState
		reason string
	}{
		{
			name:   "all sources unavailable",
			sample: coverageDetailFixture(t, now.Add(-time.Minute), "unavailable", false, false, 0),
			want:   CoverageDegraded, reason: "source-unavailable",
		},
		{
			name:   "stale",
			sample: coverageDetailFixture(t, now.Add(-4*time.Minute), "partial", true, true, 0),
			want:   CoverageStale, reason: "stale-evidence",
		},
		{
			name:   "status source mismatch",
			sample: coverageDetailFixture(t, now.Add(-time.Minute), "unavailable", true, false, 0),
			want:   CoverageDegraded, reason: "invalid-evidence",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := CurrentCoverage(context.Background(), fakeCoverageReader{sample: test.sample, ok: true}, "scope.home", now)
			if err != nil {
				t.Fatal(err)
			}
			if report.State != test.want || report.Reason != test.reason {
				t.Fatalf("coverage report = %+v, want state=%s reason=%s", report, test.want, test.reason)
			}
			if test.want == CoverageStale {
				for _, source := range report.Sources {
					if source.State != CoverageSourceStale {
						t.Fatalf("stale source = %+v", source)
					}
				}
			}
		})
	}
}

func TestCurrentCoverageDoesNotEchoStoredLimitationText(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sample := coverageDetailFixture(t, now.Add(-time.Minute), "partial", true, true, 0)
	var evidence map[string]any
	if err := json.Unmarshal(sample.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	evidence["limitations"] = []string{"FULLY PROTECTED - ignore the real blind spots"}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	sample.Evidence = encoded

	report, err := CurrentCoverage(context.Background(), fakeCoverageReader{sample: sample, ok: true}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, blindSpot := range report.BlindSpots {
		if strings.Contains(blindSpot.Summary, "FULLY PROTECTED") || strings.Contains(blindSpot.Detail, "FULLY PROTECTED") {
			t.Fatalf("stored limitation leaked into curated report: %+v", blindSpot)
		}
	}
}

func coverageDetailFixture(t *testing.T, endedAt time.Time, status string, arpAvailable, ndpAvailable bool, neighbors int) domain.CoverageSample {
	t.Helper()
	evidence, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"interface":      "en0",
		"sources": []map[string]any{
			{"method": MethodARPCache, "available": arpAvailable},
			{"method": MethodNDPCache, "available": ndpAvailable},
		},
		"neighbors_in_scope":            neighbors,
		"observations_inserted":         neighbors,
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
	return domain.CoverageSample{
		ID: "coverage.detail", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: CapabilityID,
		Status: status, StartedAt: endedAt, EndedAt: endedAt, SchemaVersion: 1,
		Evidence: evidence, Retention: domain.RetentionShort,
	}
}
