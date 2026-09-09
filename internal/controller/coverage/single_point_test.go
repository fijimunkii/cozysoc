package coverage

import "testing"

func TestValidateSinglePointReportRejectsAggregateDrift(t *testing.T) {
	report := Report{
		CapabilityID: "device-watch",
		Configured:   true,
		State:        StateDegraded,
		Reason:       "sensor-degraded",
		NextStep:     "Restore the sensor.",
		ObservationPoints: []ObservationPoint{{
			ID:       "device-watch.local",
			Kind:     "host-neighbor-cache",
			State:    StateActiveLimited,
			Reason:   "fresh-limited",
			Scope:    Scope{Configured: []Dimension{{Kind: DimensionNetwork, Value: "scope.home"}}},
			Cadence:  Cadence{Mode: CadenceUnknown},
			NextStep: "Keep the observation point running.",
		}},
	}
	if err := ValidateSinglePointReport(report); err == nil {
		t.Fatal("accepted aggregate state that drifted from the only observation point")
	}
}

func TestValidateSinglePointReportAcceptsUnconfiguredReport(t *testing.T) {
	report := Report{
		CapabilityID: "device-watch",
		Configured:   false,
		State:        StateUnconfigured,
		Reason:       "not-configured",
		NextStep:     "Configure the capability.",
	}
	if err := ValidateSinglePointReport(report); err != nil {
		t.Fatal(err)
	}
}
