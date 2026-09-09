package coverage

import (
	"testing"
	"time"
)

func TestContractRepresentsDeviceWatchWithoutTrafficClaim(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	report := Report{
		CapabilityID: "device-watch",
		Configured:   true,
		State:        StateActiveLimited,
		Reason:       "fresh-limited",
		NextStep:     "Use Traffic Watch when packet or flow visibility is required.",
		ObservationPoints: []ObservationPoint{{
			ID:       "device-watch.local-neighbor-cache",
			Kind:     "host-neighbor-cache",
			SensorID: "sensor.device-watch.home",
			State:    StateActiveLimited,
			Reason:   "fresh-limited",
			Scope: Scope{
				Configured: []Dimension{
					{Kind: DimensionNetwork, Value: "scope.home"},
					{Kind: DimensionInterface, Value: "en0"},
					{Kind: DimensionAddressFamily, Value: "ipv4"},
					{Kind: DimensionAddressFamily, Value: "ipv6"},
				},
				Verified: []Dimension{
					{Kind: DimensionNetwork, Value: "scope.home"},
					{Kind: DimensionInterface, Value: "en0"},
					{Kind: DimensionAddressFamily, Value: "ipv4"},
					{Kind: DimensionAddressFamily, Value: "ipv6"},
				},
			},
			Sources: []Source{
				{ID: "arp-cache", Kind: "neighbor-cache", State: SourceCurrent, Expected: true, Observed: true},
				{ID: "ndp-cache", Kind: "neighbor-cache", State: SourceCurrent, Expected: true, Observed: true},
			},
			Window:  EvidenceWindow{HasEvidence: true, StartedAt: now.Add(-time.Second), EndedAt: now, FreshUntil: now.Add(3 * time.Minute)},
			Cadence: Cadence{Mode: CadencePeriodic, Interval: time.Minute},
			Gaps: []Gap{{
				ID:         "no-traffic-monitoring",
				Kind:       "not-observed",
				Summary:    "Neighbor discovery is not traffic monitoring",
				Detail:     "ARP/NDP evidence does not establish packet, flow, or east-west traffic visibility.",
				NextStep:   "Use a validated traffic observation point when traffic visibility is required.",
				Directions: []Direction{DirectionIngress, DirectionEgress, DirectionEastWest},
			}},
			NextStep: "Use Traffic Watch when packet or flow visibility is required.",
		}},
	}
	if err := ValidateReport(report); err != nil {
		t.Fatal(err)
	}
	if len(report.ObservationPoints[0].Directions) != 0 {
		t.Fatal("Device Watch fixture unexpectedly claims an observed traffic direction")
	}
}

func TestContractRepresentsDNSClientsWithoutWholeNetworkAssumption(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	report := Report{
		CapabilityID: "dns-protection",
		Configured:   true,
		State:        StateActiveLimited,
		Reason:       "observed-clients-limited",
		NextStep:     "Confirm clients outside the observed resolver path or treat them as unverified.",
		ObservationPoints: []ObservationPoint{{
			ID:       "dns-protection.resolver",
			Kind:     "dns-resolver",
			SensorID: "sensor.dns.home",
			State:    StateActiveLimited,
			Reason:   "observed-clients-limited",
			Scope: Scope{
				Configured: []Dimension{
					{Kind: DimensionNetwork, Value: "scope.home"},
					{Kind: DimensionDevice, Value: "device.a"},
					{Kind: DimensionDevice, Value: "device.b"},
					{Kind: DimensionDevice, Value: "device.c"},
					{Kind: DimensionDevice, Value: "device.d"},
				},
				Verified: []Dimension{
					{Kind: DimensionNetwork, Value: "scope.home"},
					{Kind: DimensionDevice, Value: "device.a"},
					{Kind: DimensionDevice, Value: "device.b"},
					{Kind: DimensionDevice, Value: "device.c"},
				},
				ExpectedUnverified: []Dimension{{Kind: DimensionDevice, Value: "device.d"}},
			},
			Sources:    []Source{{ID: "resolver-log", Kind: "dns-resolver", State: SourceCurrent, Expected: true, Observed: true}},
			Directions: []Direction{DirectionClientToService},
			Window:     EvidenceWindow{HasEvidence: true, StartedAt: now.Add(-time.Minute), EndedAt: now, FreshUntil: now.Add(time.Minute)},
			Cadence:    Cadence{Mode: CadenceEventDriven},
			Gaps: []Gap{{
				ID:         "alternate-resolver-bypass",
				Kind:       "path-bypass",
				Summary:    "Clients outside this resolver path are not covered",
				Detail:     "Alternate resolvers or encrypted DNS can bypass the observed resolver.",
				NextStep:   "Verify resolver use per client before claiming DNS coverage for that device.",
				Dimensions: []Dimension{{Kind: DimensionDevice, Value: "device.d"}},
			}},
			NextStep: "Verify resolver use for the expected but unverified client.",
		}},
	}
	if err := ValidateReport(report); err != nil {
		t.Fatal(err)
	}
}

func TestContractRepresentsGatewayEastWestGap(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	report := Report{
		CapabilityID: "traffic-watch",
		Configured:   true,
		State:        StateActiveLimited,
		Reason:       "gateway-limited",
		NextStep:     "Add a validated observation point for local east-west traffic when required.",
		ObservationPoints: []ObservationPoint{{
			ID:       "traffic-watch.gateway",
			Kind:     "gateway-packet-sensor",
			SensorID: "sensor.gateway.home",
			State:    StateActiveLimited,
			Reason:   "gateway-limited",
			Scope: Scope{
				Configured: []Dimension{{Kind: DimensionNetwork, Value: "scope.home"}, {Kind: DimensionInterface, Value: "mirror0"}, {Kind: DimensionVLAN, Value: "10"}},
				Verified:   []Dimension{{Kind: DimensionNetwork, Value: "scope.home"}, {Kind: DimensionInterface, Value: "mirror0"}, {Kind: DimensionVLAN, Value: "10"}},
			},
			Sources:    []Source{{ID: "packet-capture", Kind: "packet-capture", State: SourceCurrent, Expected: true, Observed: true}},
			Directions: []Direction{DirectionIngress, DirectionEgress},
			Window:     EvidenceWindow{HasEvidence: true, StartedAt: now.Add(-time.Minute), EndedAt: now, FreshUntil: now.Add(time.Minute)},
			Cadence:    Cadence{Mode: CadenceContinuous},
			Gaps: []Gap{{
				ID:         "east-west-not-observed",
				Kind:       "direction-not-observed",
				Summary:    "Gateway visibility does not prove local east-west visibility",
				Detail:     "Traffic that never traverses this gateway observation point may be absent.",
				NextStep:   "Place a validated packet observation point on the local path when east-west coverage is required.",
				Directions: []Direction{DirectionEastWest},
			}},
			NextStep: "Add an observation point for east-west traffic when that direction matters.",
		}},
	}
	if err := ValidateReport(report); err != nil {
		t.Fatal(err)
	}
}

func TestContractRepresentsWirelessHoppingDwellAndEncryptionGap(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	channels := []Dimension{{Kind: DimensionWirelessChannel, Value: "1"}, {Kind: DimensionWirelessChannel, Value: "6"}, {Kind: DimensionWirelessChannel, Value: "11"}}
	report := Report{
		CapabilityID: "wireless-watch",
		Configured:   true,
		State:        StateActiveLimited,
		Reason:       "hopping-limited",
		NextStep:     "Treat unsampled time on each channel as a coverage gap.",
		ObservationPoints: []ObservationPoint{{
			ID:       "wireless-watch.radio-1",
			Kind:     "wireless-radio",
			SensorID: "sensor.radio-1",
			State:    StateActiveLimited,
			Reason:   "hopping-limited",
			Scope: Scope{
				Configured: append([]Dimension{{Kind: DimensionNetwork, Value: "scope.home"}, {Kind: DimensionWirelessBand, Value: "2.4-ghz"}}, channels...),
				Verified:   append([]Dimension{{Kind: DimensionNetwork, Value: "scope.home"}, {Kind: DimensionWirelessBand, Value: "2.4-ghz"}}, channels...),
			},
			Sources: []Source{{ID: "monitor-radio", Kind: "wireless-radio", State: SourceCurrent, Expected: true, Observed: true}},
			Window:  EvidenceWindow{HasEvidence: true, StartedAt: now.Add(-time.Minute), EndedAt: now, FreshUntil: now.Add(time.Minute)},
			Cadence: Cadence{Mode: CadenceHopping, Dwell: 250 * time.Millisecond},
			Gaps: []Gap{
				{ID: "channel-hopping-gaps", Kind: "intermittent-observation", Summary: "One hopping radio is not continuous all-channel coverage", Detail: "The radio dwells for 250 ms at a time, so frames on another channel can be missed while it is elsewhere.", NextStep: "Treat each channel as intermittently sampled or add validated radios for continuous channel coverage.", Dimensions: channels},
				{ID: "encrypted-payload", Kind: "encrypted-content", Summary: "Passive capture does not imply plaintext visibility", Detail: "Encryption can leave frame metadata observable while application payload remains unavailable.", NextStep: "Do not infer decrypted application visibility from passive wireless capture."},
			},
			NextStep: "Treat hopping channels as intermittently sampled.",
		}},
	}
	if err := ValidateReport(report); err != nil {
		t.Fatal(err)
	}
}

func TestContractRepresentsPermissionRequiredWithoutGuessingUnavailable(t *testing.T) {
	report := Report{
		CapabilityID: "traffic-watch",
		Configured:   true,
		State:        StatePermissionRequired,
		Reason:       "capture-permission-required",
		NextStep:     "Grant only the documented capture permission if this observation point is intended.",
		ObservationPoints: []ObservationPoint{{
			ID:       "traffic-watch.host",
			Kind:     "host-packet-sensor",
			State:    StatePermissionRequired,
			Reason:   "capture-permission-required",
			Scope:    Scope{Configured: []Dimension{{Kind: DimensionInterface, Value: "en0"}}},
			Sources:  []Source{{ID: "packet-capture", Kind: "packet-capture", State: SourcePermissionRequired, Expected: true, NextStep: "Grant the documented local capture permission."}},
			Window:   EvidenceWindow{},
			Cadence:  Cadence{Mode: CadenceContinuous},
			NextStep: "Grant the documented local capture permission.",
		}},
	}
	if err := ValidateReport(report); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReportRejectsConflatedOrImpossibleScope(t *testing.T) {
	base := Report{
		CapabilityID: "device-watch",
		Configured:   true,
		State:        StateUnverified,
		Reason:       "no-evidence",
		NextStep:     "Wait for evidence.",
		ObservationPoints: []ObservationPoint{{
			ID:       "device-watch.local",
			Kind:     "host-neighbor-cache",
			State:    StateUnverified,
			Reason:   "no-evidence",
			Scope:    Scope{Configured: []Dimension{{Kind: DimensionNetwork, Value: "scope.home"}}},
			Window:   EvidenceWindow{},
			Cadence:  Cadence{Mode: CadencePeriodic, Interval: time.Minute},
			NextStep: "Wait for evidence.",
		}},
	}

	t.Run("expected unverified must be configured", func(t *testing.T) {
		report := base
		report.ObservationPoints = append([]ObservationPoint(nil), base.ObservationPoints...)
		report.ObservationPoints[0].Scope.ExpectedUnverified = []Dimension{{Kind: DimensionVLAN, Value: "20"}}
		if err := ValidateReport(report); err == nil {
			t.Fatal("accepted expected-unverified dimension outside configured scope")
		}
	})

	t.Run("verified cannot also be expected unverified", func(t *testing.T) {
		report := base
		report.ObservationPoints = append([]ObservationPoint(nil), base.ObservationPoints...)
		dimension := Dimension{Kind: DimensionNetwork, Value: "scope.home"}
		report.ObservationPoints[0].Scope.Verified = []Dimension{dimension}
		report.ObservationPoints[0].Scope.ExpectedUnverified = []Dimension{dimension}
		if err := ValidateReport(report); err == nil {
			t.Fatal("accepted dimension as both verified and expected-unverified")
		}
	})

	t.Run("unconfigured cannot carry points", func(t *testing.T) {
		report := base
		report.Configured = false
		report.State = StateUnconfigured
		if err := ValidateReport(report); err == nil {
			t.Fatal("accepted observation points on unconfigured report")
		}
	})

	t.Run("hopping requires dwell", func(t *testing.T) {
		report := base
		report.ObservationPoints = append([]ObservationPoint(nil), base.ObservationPoints...)
		report.ObservationPoints[0].Cadence = Cadence{Mode: CadenceHopping}
		if err := ValidateReport(report); err == nil {
			t.Fatal("accepted hopping cadence without dwell")
		}
	})
}
