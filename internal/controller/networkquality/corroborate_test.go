package networkquality

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func comparisonFixture() CorroborationInput {
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	observer := Observer{ScopeID: "scope.home", SensorID: "sensor.network", InterfaceName: "en0", InterfaceIndex: 7}
	selection := ResolverSelection{ID: "selection.test", ResolverID: "resolver.test", QueryID: "query.test", Family: FamilyIPv4, Transport: DNSUDP, QueryType: DNSQueryAAAA, Expect: DNSExpectAnswer}
	in := CorroborationInput{NetworkDeviceID: "device.local", ResolverDeviceID: "device.local", Family: FamilyIPv4, MaxCompletionSkew: 5 * time.Second,
		Network: Snapshot{Observer: observer, AsOf: at, WindowStart: at.Add(-time.Hour), Freshness: time.Minute,
			Targets:      []Target{{ID: "target.icmp", Layer: LayerGateway, Method: MethodICMP, Family: FamilyIPv4}},
			Measurements: []Measurement{{ID: "measurement.icmp", TargetID: "target.icmp", Observer: observer, StartedAt: at.Add(-5 * time.Second), CompletedAt: at.Add(-2 * time.Second), Outcome: OutcomeSucceeded, Attempts: 3, Successes: 3}}},
		Resolvers: ResolverSnapshot{Observer: observer, AsOf: at, WindowStart: at.Add(-time.Hour), Freshness: time.Minute, Selections: []ResolverSelection{selection}}}
	in.Resolvers.Observer.SensorID = "sensor.resolver"
	in.Resolvers.Measurements = []ResolverMeasurement{{ID: "measurement.dns", Selection: selection, Observer: in.Resolvers.Observer, StartedAt: at.Add(-3 * time.Second), CompletedAt: at.Add(-time.Second), Exchange: DNSResponseReceived, Request: DNSRequestAccepted, Reply: &DNSReply{Answer: DNSAnswerPresent}}}
	return in
}
func failICMP(in *CorroborationInput) {
	in.Network.Measurements[0].Outcome = OutcomeFailed
	in.Network.Measurements[0].Successes = 0
}
func timeoutDNS(in *CorroborationInput) {
	m := &in.Resolvers.Measurements[0]
	m.Exchange = DNSTimeout
	m.Reply = nil
}
func negativeDNS(in *CorroborationInput, expected bool) {
	m := &in.Resolvers.Measurements[0]
	m.Reply = &DNSReply{RCode: 3}
	if expected {
		in.Resolvers.Selections[0].Expect = DNSExpectNXDOMAIN
		m.Selection = in.Resolvers.Selections[0]
	}
}
func addLink(in *CorroborationInput, up bool) {
	outcome := OutcomeSucceeded
	if !up {
		outcome = OutcomeFailed
	}
	in.Network.Targets = append(in.Network.Targets, Target{ID: "target.link", Layer: LayerLink, Method: MethodInterface, Family: FamilyNone})
	in.Network.Measurements = append(in.Network.Measurements, Measurement{ID: "measurement.link", TargetID: "target.link", Observer: in.Network.Observer, StartedAt: in.Network.AsOf.Add(-2 * time.Second), CompletedAt: in.Network.AsOf.Add(-2 * time.Second), Outcome: outcome})
}
func TestCorroborationKeepsAssociationsLimitedToSelectedChecks(t *testing.T) {
	cases := []struct {
		name, want string
		edit       func(*CorroborationInput)
	}{
		{"matched", "selected-checks-matched", func(*CorroborationInput) {}},
		{"expected NXDOMAIN", "selected-checks-matched", func(in *CorroborationInput) { negativeDNS(in, true) }},
		{"unexpected NXDOMAIN", "dns-query-issue-with-responses", func(in *CorroborationInput) { negativeDNS(in, false) }},
		{"DNS timeout with ICMP replies", "dns-query-issue-with-responses", timeoutDNS},
		{"ICMP silence with DNS answer", "icmp-misses-with-responses", failICMP},
		{"ICMP silence with expected NXDOMAIN", "icmp-misses-with-responses", func(in *CorroborationInput) { failICMP(in); negativeDNS(in, true) }},
		{"ICMP silence with SERVFAIL reply", "icmp-misses-with-responses", func(in *CorroborationInput) { failICMP(in); in.Resolvers.Measurements[0].Reply = &DNSReply{RCode: 2} }},
		{"two silent checks", "problems-across-selected-layers", func(in *CorroborationInput) { failICMP(in); timeoutDNS(in) }},
		{"link down and silence", "local-link-issue-with-other-failures", func(in *CorroborationInput) { addLink(in, false); failICMP(in); timeoutDNS(in) }},
		{"link down and response", "mixed-link-evidence", func(in *CorroborationInput) { addLink(in, false) }},
		{"external failure with other replies", "external-check-issue-with-responses", func(in *CorroborationInput) {
			addHTTPS(in, 503)
		}},
		{"incomplete DNS never becomes timeout", "insufficient-evidence", func(in *CorroborationInput) {
			m := &in.Resolvers.Measurements[0]
			m.Exchange = DNSIncomplete
			m.Request = DNSRequestUncertain
			m.Reply = nil
		}},
		{"truncated reply with ICMP silence", "icmp-misses-with-responses", func(in *CorroborationInput) {
			failICMP(in)
			in.Resolvers.Measurements[0].Reply = &DNSReply{Truncated: true}
		}},
		{"single external failure", "insufficient-evidence", func(in *CorroborationInput) {
			in.Resolvers.Selections = nil
			in.Resolvers.Measurements = nil
			in.Network.Targets[0].Layer = LayerExternal
			failICMP(in)
		}},
		{"ICMP cannot corroborate its own partial replies", "mixed-or-limited-evidence", func(in *CorroborationInput) {
			in.Resolvers.Selections = nil
			in.Resolvers.Measurements = nil
			addLink(in, true)
			in.Network.Measurements[0].Outcome = OutcomePartial
			in.Network.Measurements[0].Successes = 1
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := comparisonFixture()
			tc.edit(&in)
			out, err := Corroborate(in)
			if err != nil || out.Conclusion != tc.want {
				t.Fatalf("%+v %v", out, err)
			}
			if out.Confidence == ConfidenceUnknown && len(out.Compared) != 0 {
				t.Fatal("unknown comparison claims supporting measurements")
			}
			if out.Confidence != ConfidenceUnknown && out.Confidence != ConfidenceLimited {
				t.Fatal("inflated confidence")
			}
			if strings.Contains(out.Conclusion, "internet") || strings.Contains(out.Conclusion, "secure") || strings.Contains(out.Conclusion, "outage") {
				t.Fatal("global verdict")
			}
			if out.Summary == "" || out.NextStep == "" || len(out.Limitations) == 0 {
				t.Fatal("missing explanation")
			}
		})
	}
}
func TestCorroborationRequiresTemporalAndLocationAgreement(t *testing.T) {
	for name, edit := range map[string]func(*CorroborationInput){
		"different device": func(in *CorroborationInput) { in.ResolverDeviceID = "device.other" },
		"missing device":   func(in *CorroborationInput) { in.NetworkDeviceID = ""; in.ResolverDeviceID = "" },
		"scope":            func(in *CorroborationInput) { in.Resolvers.Observer.ScopeID = "scope.other" },
		"interface":        func(in *CorroborationInput) { in.Resolvers.Observer.InterfaceName = "en1" },
		"index":            func(in *CorroborationInput) { in.Resolvers.Observer.InterfaceIndex++ },
		"read time":        func(in *CorroborationInput) { in.Resolvers.AsOf = in.Resolvers.AsOf.Add(time.Second) },
		"window":           func(in *CorroborationInput) { in.Resolvers.WindowStart = in.Resolvers.WindowStart.Add(time.Second) },
		"freshness":        func(in *CorroborationInput) { in.Resolvers.Freshness += time.Second },
		"zero skew":        func(in *CorroborationInput) { in.MaxCompletionSkew = 0 },
		"unbounded skew":   func(in *CorroborationInput) { in.MaxCompletionSkew = MaxCorroborationSkew + 1 },
		"family":           func(in *CorroborationInput) { in.Family = FamilyNone },
		"generic DNS": func(in *CorroborationInput) {
			in.Network.Targets[0].Layer = LayerDNS
			in.Network.Targets[0].Method = MethodDNS
		},
		"duplicate measurement across collectors": func(in *CorroborationInput) { in.Resolvers.Measurements[0].ID = in.Network.Measurements[0].ID },
		"invalid DNS reply":                       func(in *CorroborationInput) { in.Resolvers.Measurements[0].Reply.RCode = -1 },
		"ambiguous latest": func(in *CorroborationInput) {
			m := in.Network.Measurements[0]
			m.ID = "measurement.other"
			in.Network.Measurements = append(in.Network.Measurements, m)
		},
		"combined target bound": func(in *CorroborationInput) {
			for i := 1; i < MaxTargets; i++ {
				target := in.Network.Targets[0]
				target.ID += "." + strings.Repeat("a", i)
				in.Network.Targets = append(in.Network.Targets, target)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := comparisonFixture()
			edit(&in)
			out, err := Corroborate(in)
			if err == nil || !reflect.DeepEqual(out, Corroboration{}) {
				t.Fatalf("published invalid input: %+v %v", out, err)
			}
		})
	}
	in := comparisonFixture()
	in.Family = FamilyIPv6
	out, err := Corroborate(in)
	if err != nil || len(out.Evidence) != 0 || out.Conclusion != "insufficient-evidence" {
		t.Fatal("borrowed IPv4 evidence", err)
	}
	in = comparisonFixture()
	in.Resolvers.Selections[0].Family = FamilyIPv6
	in.Resolvers.Measurements[0].Selection = in.Resolvers.Selections[0]
	out, err = Corroborate(in)
	if err != nil || len(out.Compared) != 0 {
		t.Fatal("compared across transport families", err)
	}
}
func TestCorroborationFreshnessSkewAndExplicitGaps(t *testing.T) {
	in := comparisonFixture()
	in.MaxCompletionSkew = time.Second
	out, err := Corroborate(in)
	if err != nil || out.Conclusion != "selected-checks-matched" {
		t.Fatal("exact skew boundary", err)
	}
	in.Network.Measurements[0].CompletedAt = in.Network.Measurements[0].CompletedAt.Add(-time.Nanosecond)
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "observations-too-far-apart" || len(out.Compared) != 0 {
		t.Fatal("combined separated samples", err)
	}
	in = comparisonFixture()
	in.Network.Freshness = 2 * time.Second
	in.Resolvers.Freshness = 2 * time.Second
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "insufficient-evidence" {
		t.Fatal("fresh-until boundary", err)
	}
	for _, gap := range []GapReason{GapSleep, GapOffline, GapNetworkChanged} {
		in = comparisonFixture()
		addLink(&in, true)
		m := &in.Network.Measurements[1]
		m.Outcome = OutcomeNotRun
		m.Gap = gap
		out, err = Corroborate(in)
		if err != nil || out.Conclusion != "collection-discontinuity" || len(out.Compared) != 0 {
			t.Fatalf("gap %s: %+v %v", gap, out, err)
		}
		// A recovered sample must start strictly after the recorded discontinuity.
		m.StartedAt = in.Network.AsOf.Add(-10 * time.Second)
		m.CompletedAt = m.StartedAt
		out, err = Corroborate(in)
		if err != nil || out.Conclusion != "selected-checks-matched" {
			t.Fatalf("recovered gap: %+v %v", out, err)
		}
	}
	// A newer explicit gap supersedes a prior successful DNS sample; it must not
	// search the older sample to manufacture a comparison.
	in = comparisonFixture()
	m := in.Resolvers.Measurements[0]
	m.ID = "measurement.dns-gap"
	m.StartedAt = in.Resolvers.AsOf
	m.CompletedAt = m.StartedAt
	m.Exchange = DNSNotMeasured
	m.Request = DNSRequestNotSent
	m.Gap = GapDisabled
	m.Reply = nil
	in.Resolvers.Measurements = append(in.Resolvers.Measurements, m)
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "insufficient-evidence" {
		t.Fatal("selected older DNS success", err)
	}
}
func TestCorroborationDeterministicAndOwnsEvidence(t *testing.T) {
	in := comparisonFixture()
	negativeDNS(&in, true)
	addLink(&in, true)
	old := in.Network.Measurements[0]
	old.ID = "measurement.old"
	old.StartedAt = old.StartedAt.Add(-time.Minute)
	old.CompletedAt = old.CompletedAt.Add(-time.Minute)
	old.Outcome = OutcomeFailed
	old.Successes = 0
	in.Network.Measurements = append(in.Network.Measurements, old)
	first, err := Corroborate(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Network.Targets[0], in.Network.Targets[1] = in.Network.Targets[1], in.Network.Targets[0]
	in.Network.Measurements[0], in.Network.Measurements[2] = in.Network.Measurements[2], in.Network.Measurements[0]
	second, err := Corroborate(in)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("order dependent comparison", err)
	}
	for i := range first.Evidence {
		if e := &first.Evidence[i]; e.Layer == LayerDNS {
			if e.Selection == nil || e.Selection.Expect != DNSExpectNXDOMAIN || e.ExpectationMatched == nil || !*e.ExpectationMatched {
				t.Fatal("lost recorded expectation")
			}
			e.Selection.Expect = DNSExpectAnswer
			*e.ExpectationMatched = false
		}
	}
	third, err := Corroborate(in)
	if err != nil || !reflect.DeepEqual(second, third) {
		t.Fatal("aliased input", err)
	}
	if !first.EvidenceStart.Equal(in.Network.AsOf.Add(-5*time.Second)) || !first.EvidenceEnd.Equal(in.Network.AsOf.Add(-time.Second)) {
		t.Fatal("rewrote original evidence time")
	}
}

func TestCorroborationRecoveryDoesNotEraseEarlierDiscontinuity(t *testing.T) {
	in := comparisonFixture()
	addLink(&in, true)
	gap := in.Network.Measurements[1]
	gap.ID = "measurement.link-gap"
	gap.Outcome = OutcomeNotRun
	gap.Gap = GapNetworkChanged
	gap.StartedAt = in.Network.AsOf.Add(-4 * time.Second)
	gap.CompletedAt = gap.StartedAt
	in.Network.Measurements = append(in.Network.Measurements, gap)
	out, err := Corroborate(in)
	if err != nil || out.Conclusion != "collection-discontinuity" || out.DiscontinuityID != gap.ID || !out.DiscontinuityAt.Equal(gap.CompletedAt) || out.DiscontinuityGap != GapNetworkChanged {
		t.Fatalf("erased binding gap: %+v %v", out, err)
	}
	// The latest link result is up, but the old ICMP sample began before the gap.
	in.Network.Measurements[0].StartedAt = in.Network.AsOf.Add(-3 * time.Second)
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "selected-checks-matched" || out.DiscontinuityID != "" {
		t.Fatalf("failed to recover after all samples restarted: %+v %v", out, err)
	}
}

func TestCorroborationOtherFamilyGapStillInvalidatesObservationPoint(t *testing.T) {
	in := comparisonFixture()
	addLink(&in, true)
	s := in.Resolvers.Selections[0]
	s.ID = "selection.ipv6"
	s.Family = FamilyIPv6
	in.Resolvers.Selections = append(in.Resolvers.Selections, s)
	in.Resolvers.Measurements = append(in.Resolvers.Measurements, ResolverMeasurement{ID: "measurement.ipv6-gap", Selection: s, Observer: in.Resolvers.Observer, StartedAt: in.Resolvers.AsOf.Add(-time.Second), CompletedAt: in.Resolvers.AsOf.Add(-time.Second), Exchange: DNSNotMeasured, Request: DNSRequestNotSent, Gap: GapSleep})
	out, err := Corroborate(in)
	if err != nil || out.Conclusion != "collection-discontinuity" || out.DiscontinuityID != "measurement.ipv6-gap" {
		t.Fatalf("ignored observation-point gap: %+v %v", out, err)
	}
	for _, e := range out.Evidence {
		if e.Family == FamilyIPv6 {
			t.Fatal("borrowed another family's check results")
		}
	}
}
