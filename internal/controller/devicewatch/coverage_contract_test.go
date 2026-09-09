package devicewatch

import (
	"context"
	"testing"
	"time"

	sharedcoverage "github.com/fijimunkii/cozysoc/internal/controller/coverage"
)

func TestDeviceWatchCoverageContractSeparatesConfiguredVerifiedAndExpectedScope(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sample := coverageDetailFixture(t, now.Add(-time.Minute), "partial", true, false, 1)
	report, err := CurrentCoverage(context.Background(), fakeCoverageReader{sample: sample, ok: true}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}

	contract, err := CoverageContract(report, healthyCoverageOperational())
	if err != nil {
		t.Fatal(err)
	}
	if contract.State != sharedcoverage.StateDegraded || contract.Reason != "source-partial" || len(contract.ObservationPoints) != 1 {
		t.Fatalf("shared coverage = %+v", contract)
	}
	point := contract.ObservationPoints[0]
	if point.ID != deviceWatchObservationPointID || point.Kind != "host-neighbor-cache" || len(point.Directions) != 0 {
		t.Fatalf("observation point identity/directions = %+v", point)
	}
	if !hasSharedDimension(point.Scope.Configured, sharedcoverage.DimensionNetwork, "scope.home") || !hasSharedDimension(point.Scope.Configured, sharedcoverage.DimensionInterface, "en0") {
		t.Fatalf("configured scope = %+v", point.Scope)
	}
	if !hasSharedDimension(point.Scope.Verified, sharedcoverage.DimensionAddressFamily, "ipv4") || hasSharedDimension(point.Scope.Verified, sharedcoverage.DimensionAddressFamily, "ipv6") {
		t.Fatalf("verified scope = %+v", point.Scope.Verified)
	}
	if !hasSharedDimension(point.Scope.ExpectedUnverified, sharedcoverage.DimensionAddressFamily, "ipv6") {
		t.Fatalf("expected-unverified scope = %+v", point.Scope.ExpectedUnverified)
	}
	if !point.Window.HasEvidence || !point.Window.StartedAt.Equal(report.EvidenceAt) || !point.Window.EndedAt.Equal(report.EvidenceAt) || !point.Window.FreshUntil.Equal(report.FreshUntil) {
		t.Fatalf("evidence window = %+v", point.Window)
	}
	if point.Cadence.Mode != sharedcoverage.CadencePeriodic || point.Cadence.Interval != defaultCollectionInterval {
		t.Fatalf("cadence = %+v", point.Cadence)
	}
	if len(point.Gaps) != 3 || point.Gaps[2].ID != "no-traffic-monitoring" || len(point.Gaps[2].Directions) != 3 {
		t.Fatalf("shared gaps = %+v", point.Gaps)
	}
}

func TestDeviceWatchCoverageContractKeepsNoEvidenceExpectedUnverified(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	report, err := CurrentCoverage(context.Background(), fakeCoverageReader{}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := CoverageContract(report, healthyCoverageOperational())
	if err != nil {
		t.Fatal(err)
	}
	point := contract.ObservationPoints[0]
	if contract.State != sharedcoverage.StateUnverified || point.Window.HasEvidence || len(point.Scope.Verified) != 0 {
		t.Fatalf("no-evidence contract = %+v", contract)
	}
	for _, value := range []string{"scope.home", "ipv4", "ipv6"} {
		if !hasSharedValue(point.Scope.ExpectedUnverified, value) {
			t.Fatalf("missing expected-unverified dimension %q: %+v", value, point.Scope.ExpectedUnverified)
		}
	}
	for _, source := range point.Sources {
		if source.State != sharedcoverage.SourceExpectedUnverified || source.Observed {
			t.Fatalf("missing source projection = %+v", source)
		}
	}
}

func TestDeviceWatchCoverageContractMakesMeasuredLagAggregateState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sample := coverageDetailFixture(t, now.Add(-time.Minute), "partial", true, true, 0)
	report, err := CurrentCoverage(context.Background(), fakeCoverageReader{sample: sample, ok: true}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	operational := healthyCoverageOperational()
	operational.Pipeline = PipelineHealth{State: OperationalDegraded, Reason: "latency", NextStep: "Restore timely durable ingestion."}
	contract, err := CoverageContract(report, operational)
	if err != nil {
		t.Fatal(err)
	}
	if contract.State != sharedcoverage.StateDegraded || contract.Reason != "ingestion-latency" || contract.NextStep != "Restore timely durable ingestion." {
		t.Fatalf("latency contract = %+v", contract)
	}
	point := contract.ObservationPoints[0]
	if !hasSharedDimension(point.Scope.Verified, sharedcoverage.DimensionAddressFamily, "ipv4") || !hasSharedDimension(point.Scope.Verified, sharedcoverage.DimensionAddressFamily, "ipv6") {
		t.Fatalf("operational degradation erased verified evidence scope: %+v", point.Scope)
	}
}

func TestUnconfiguredDeviceWatchCoverageContractIsValid(t *testing.T) {
	contract := UnconfiguredCoverageContract()
	if err := sharedcoverage.ValidateReport(contract); err != nil {
		t.Fatal(err)
	}
	if contract.Configured || contract.State != sharedcoverage.StateUnconfigured || len(contract.ObservationPoints) != 0 {
		t.Fatalf("unconfigured contract = %+v", contract)
	}
}

func healthyCoverageOperational() OperationalHealth {
	return OperationalHealth{
		Sensor:   SensorHealth{State: OperationalCurrent},
		Pipeline: PipelineHealth{State: OperationalCurrent},
		Database: DatabaseHealth{State: OperationalCurrent},
	}
}

func hasSharedDimension(dimensions []sharedcoverage.Dimension, kind sharedcoverage.DimensionKind, value string) bool {
	for _, dimension := range dimensions {
		if dimension.Kind == kind && dimension.Value == value {
			return true
		}
	}
	return false
}

func hasSharedValue(dimensions []sharedcoverage.Dimension, value string) bool {
	for _, dimension := range dimensions {
		if dimension.Value == value {
			return true
		}
	}
	return false
}
