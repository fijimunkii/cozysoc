package devicewatch

import (
	"fmt"

	sharedcoverage "github.com/fijimunkii/cozysoc/internal/controller/coverage"
)

const deviceWatchObservationPointID = "device-watch.local-neighbor-cache"

func CoverageContract(report CoverageReport, operational OperationalHealth) (sharedcoverage.Report, error) {
	state, reason, nextStep := EffectiveCoverage(report, operational)
	point := sharedcoverage.ObservationPoint{
		ID:       deviceWatchObservationPointID,
		Kind:     "host-neighbor-cache",
		SensorID: report.SensorID,
		State:    sharedcoverage.State(state),
		Reason:   reason,
		Scope:    deviceWatchCoverageScope(report),
		Sources:  make([]sharedcoverage.Source, 0, len(report.Sources)),
		Window: sharedcoverage.EvidenceWindow{
			HasEvidence: report.HasEvidence,
		},
		Cadence:  sharedcoverage.Cadence{Mode: sharedcoverage.CadencePeriodic, Interval: defaultCollectionInterval},
		Gaps:     make([]sharedcoverage.Gap, 0, len(report.BlindSpots)),
		NextStep: nextStep,
	}
	if report.HasEvidence {
		// Device Watch currently records a snapshot instant for this coverage
		// sample; keeping start=end is narrower than inventing an observed
		// interval that the source did not establish.
		point.Window.StartedAt = report.EvidenceAt
		point.Window.EndedAt = report.EvidenceAt
		point.Window.FreshUntil = report.FreshUntil
	}
	for _, source := range report.Sources {
		point.Sources = append(point.Sources, sharedcoverage.Source{
			ID:       source.ID,
			Kind:     "neighbor-cache",
			State:    sharedSourceState(source.State),
			Expected: true,
			Observed: source.Observed,
			NextStep: source.NextStep,
		})
	}
	for _, blindSpot := range report.BlindSpots {
		point.Gaps = append(point.Gaps, sharedDeviceWatchGap(blindSpot))
	}

	contract := sharedcoverage.Report{
		CapabilityID:      CapabilityID,
		Configured:        true,
		State:             sharedcoverage.State(state),
		Reason:            reason,
		ObservationPoints: []sharedcoverage.ObservationPoint{point},
		NextStep:          nextStep,
	}
	if err := sharedcoverage.ValidateReport(contract); err != nil {
		return sharedcoverage.Report{}, fmt.Errorf("validate Device Watch coverage contract: %w", err)
	}
	return contract, nil
}

func UnconfiguredCoverageContract() sharedcoverage.Report {
	return sharedcoverage.Report{
		CapabilityID: CapabilityID,
		Configured:   false,
		State:        sharedcoverage.StateUnconfigured,
		Reason:       "not-configured",
		NextStep:     "Enroll a home network and enable Device Watch before evaluating its coverage.",
	}
}

func deviceWatchCoverageScope(report CoverageReport) sharedcoverage.Scope {
	network := sharedcoverage.Dimension{Kind: sharedcoverage.DimensionNetwork, Value: report.ScopeID}
	ipv4 := sharedcoverage.Dimension{Kind: sharedcoverage.DimensionAddressFamily, Value: "ipv4"}
	ipv6 := sharedcoverage.Dimension{Kind: sharedcoverage.DimensionAddressFamily, Value: "ipv6"}
	configured := []sharedcoverage.Dimension{network, ipv4, ipv6}
	var iface *sharedcoverage.Dimension
	if report.InterfaceName != "" {
		value := sharedcoverage.Dimension{Kind: sharedcoverage.DimensionInterface, Value: report.InterfaceName}
		configured = append(configured, value)
		iface = &value
	}

	verified := make([]sharedcoverage.Dimension, 0, len(configured))
	expected := make([]sharedcoverage.Dimension, 0, len(configured))
	currentFamilies := 0
	for _, source := range report.Sources {
		dimension, ok := addressFamilyDimension(source.AddressFamily)
		if !ok {
			continue
		}
		if source.State == CoverageSourceCurrent {
			verified = append(verified, dimension)
			currentFamilies++
		} else {
			expected = append(expected, dimension)
		}
	}

	if currentFamilies > 0 {
		verified = append(verified, network)
		if iface != nil {
			verified = append(verified, *iface)
		}
	} else {
		expected = append(expected, network)
		if iface != nil {
			expected = append(expected, *iface)
		}
	}
	return sharedcoverage.Scope{
		Configured:         configured,
		Verified:           verified,
		ExpectedUnverified: expected,
	}
}

func addressFamilyDimension(family string) (sharedcoverage.Dimension, bool) {
	switch family {
	case "ipv4", "ipv6":
		return sharedcoverage.Dimension{Kind: sharedcoverage.DimensionAddressFamily, Value: family}, true
	default:
		return sharedcoverage.Dimension{}, false
	}
}

func sharedSourceState(state CoverageSourceState) sharedcoverage.SourceState {
	switch state {
	case CoverageSourceCurrent:
		return sharedcoverage.SourceCurrent
	case CoverageSourceUnavailable:
		return sharedcoverage.SourceUnavailable
	case CoverageSourceStale:
		return sharedcoverage.SourceStale
	case CoverageSourceMissing:
		return sharedcoverage.SourceExpectedUnverified
	default:
		return sharedcoverage.SourceUnknown
	}
}

func sharedDeviceWatchGap(blindSpot CoverageBlindSpot) sharedcoverage.Gap {
	gap := sharedcoverage.Gap{
		ID:       blindSpot.ID,
		Kind:     "known-exclusion",
		Summary:  blindSpot.Summary,
		Detail:   blindSpot.Detail,
		NextStep: blindSpot.NextStep,
	}
	switch blindSpot.ID {
	case "host-neighbor-cache-only":
		gap.Kind = "observation-point-limited"
	case "isolated-segments-not-observed":
		gap.Kind = "scope-not-observed"
	case "no-traffic-monitoring":
		gap.Kind = "direction-not-observed"
		gap.Directions = []sharedcoverage.Direction{
			sharedcoverage.DirectionIngress,
			sharedcoverage.DirectionEgress,
			sharedcoverage.DirectionEastWest,
		}
	}
	return gap
}
