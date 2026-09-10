package networkquality

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// All fixtures are synthetic. Target IDs are opaque references, not real hosts;
// none of these tests performs a lookup, network check, or process execution.
func fixture(target Target, outcome Outcome) Snapshot {
	asOf := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	observer := Observer{ScopeID: "scope-home", SensorID: "sensor-desktop", InterfaceName: "en0", InterfaceIndex: 1}
	measurement := Measurement{
		ID: "measurement-1", TargetID: target.ID, Observer: observer,
		StartedAt: asOf.Add(-3 * time.Second), CompletedAt: asOf.Add(-time.Second),
		Outcome: outcome,
	}
	if target.Method != MethodInterface {
		measurement.Attempts = 4
		switch outcome {
		case OutcomeSucceeded:
			measurement.Successes = 4
		case OutcomePartial:
			measurement.Successes = 3
		}
	}
	return Snapshot{
		Observer: observer, AsOf: asOf, WindowStart: asOf.Add(-time.Hour), Freshness: 3 * time.Minute,
		Targets: []Target{target}, Measurements: []Measurement{measurement},
	}
}

func gateway() Target {
	return Target{ID: "gateway-v4", Layer: LayerGateway, Method: MethodICMP, Family: FamilyIPv4}
}

func TestSyntheticDiagnosisMatrix(t *testing.T) {
	cases := []struct {
		name    string
		target  Target
		outcome Outcome
		state   State
		summary string
		loss    bool
	}{
		{"interface up is not internet", Target{"link", LayerLink, MethodInterface, FamilyNone}, OutcomeSucceeded, StateSucceeded, "interface reported up", false},
		{"interface down is local", Target{"link", LayerLink, MethodInterface, FamilyNone}, OutcomeFailed, StateIssueObserved, "interface reported down", false},
		{"gateway answers", gateway(), OutcomeSucceeded, StateSucceeded, "gateway answered", true},
		{"gateway does not answer", gateway(), OutcomeFailed, StateIssueObserved, "gateway did not answer", true},
		{"probe reply loss", gateway(), OutcomePartial, StateIssueObserved, "Some ICMP probes", true},
		{"DNS works", Target{"resolver-v4", LayerDNS, MethodDNS, FamilyIPv4}, OutcomeSucceeded, StateSucceeded, "resolver completed", false},
		{"DNS failure", Target{"resolver-v4", LayerDNS, MethodDNS, FamilyIPv4}, OutcomeFailed, StateIssueObserved, "DNS check to", false},
		{"partial DNS is not packet loss", Target{"resolver-v4", LayerDNS, MethodDNS, FamilyIPv4}, OutcomePartial, StateIssueObserved, "Some DNS checks", false},
		{"external works", Target{"external-v4", LayerExternal, MethodHTTPS, FamilyIPv4}, OutcomeSucceeded, StateSucceeded, "external target passed", false},
		{"single endpoint failure", Target{"external-v4", LayerExternal, MethodHTTPS, FamilyIPv4}, OutcomeFailed, StateIssueObserved, "external target did not pass", false},
		{"partial HTTPS", Target{"external-v4", LayerExternal, MethodHTTPS, FamilyIPv4}, OutcomePartial, StateIssueObserved, "Some HTTPS checks", false},
		{"external ICMP failure", Target{"external-v6", LayerExternal, MethodICMP, FamilyIPv6}, OutcomeFailed, StateIssueObserved, "external target did not pass", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := fixture(tc.target, tc.outcome)
			report, err := Assess(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			if check.State != tc.state || check.Confidence != ConfidenceLimited || !strings.Contains(check.Summary, tc.summary) {
				t.Fatalf("unexpected assessment: %+v", check)
			}
			if check.Target != tc.target || check.EvidenceID != "measurement-1" || check.NextStep == "" || report.Observer != snapshot.Observer {
				t.Fatalf("missing context, evidence, or guidance: %+v", report)
			}
			if (check.ReplyLossPercent != nil) != tc.loss || check.MeanLatency != nil {
				t.Fatal("invented or missing metrics")
			}
			if tc.loss {
				want := 100 * float64(4-snapshot.Measurements[0].Successes) / 4
				if *check.ReplyLossPercent != want {
					t.Fatalf("reply loss = %v, want %v", *check.ReplyLossPercent, want)
				}
			}
			if len(report.Limitations) != 3 || !strings.Contains(report.Limitations[1], "separate from security") {
				t.Fatal("quality lost its concern-separation limits")
			}
		})
	}
}

func TestExplicitGapsAreNotNetworkFailures(t *testing.T) {
	cases := []struct {
		gap     GapReason
		outcome Outcome
	}{
		{GapPermission, OutcomeUnavailable}, {GapUnsupported, OutcomeUnavailable},
		{GapSourceUnavailable, OutcomeUnavailable}, {GapDisabled, OutcomeNotRun},
		{GapNotConfigured, OutcomeNotRun}, {GapSleep, OutcomeNotRun},
		{GapOffline, OutcomeNotRun}, {GapNetworkChanged, OutcomeNotRun},
	}
	for _, tc := range cases {
		t.Run(string(tc.gap), func(t *testing.T) {
			snapshot := fixture(gateway(), OutcomeSucceeded)
			previous := snapshot.Measurements[0]
			previous.ID = "previous-success"
			previous.StartedAt = previous.StartedAt.Add(-5 * time.Minute)
			previous.CompletedAt = previous.CompletedAt.Add(-5 * time.Minute)
			gap := &snapshot.Measurements[0]
			gap.Outcome, gap.Gap = tc.outcome, tc.gap
			gap.Attempts, gap.Successes = 0, 0
			// A gap may describe minutes of downtime, not a running probe.
			gap.StartedAt = gap.CompletedAt.Add(-4 * time.Minute)
			snapshot.Measurements = append(snapshot.Measurements, previous)
			report, err := Assess(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			if check.State != StateNotMeasured || check.Gap != tc.gap || check.Confidence != ConfidenceUnknown ||
				check.EvidenceID != "measurement-1" || check.MeanLatency != nil || check.ReplyLossPercent != nil || check.NextStep == "" {
				t.Fatalf("gap became a network result or retained prior success: %+v", check)
			}
		})
	}
}

func TestFreshnessAndReplayUseEvidenceTime(t *testing.T) {
	for _, age := range []time.Duration{3*time.Minute - time.Nanosecond, 3 * time.Minute, 4 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			snapshot := fixture(gateway(), OutcomeSucceeded)
			measurement := &snapshot.Measurements[0]
			measurement.CompletedAt = snapshot.AsOf.Add(-age)
			measurement.StartedAt = measurement.CompletedAt.Add(-time.Second)
			report, err := Assess(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			want := StateSucceeded
			if age >= snapshot.Freshness {
				want = StateStale
				if check.MeanLatency != nil || check.ReplyLossPercent != nil || check.Confidence != ConfidenceUnknown {
					t.Fatal("historical metrics presented as current")
				}
			}
			if check.State != want || !check.FreshUntil.Equal(measurement.CompletedAt.Add(snapshot.Freshness)) {
				t.Fatalf("age %v: %+v", age, check)
			}
		})
	}
	snapshot := fixture(gateway(), OutcomeFailed)
	older := snapshot.Measurements[0]
	older.ID, older.Outcome, older.Successes = "replayed-success", OutcomeSucceeded, 4
	older.StartedAt, older.CompletedAt = older.StartedAt.Add(-time.Minute), older.CompletedAt.Add(-time.Minute)
	snapshot.Measurements = append(snapshot.Measurements, older)
	first, err := Assess(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Measurements[0], snapshot.Measurements[1] = snapshot.Measurements[1], snapshot.Measurements[0]
	second, err := Assess(snapshot)
	if err != nil || !reflect.DeepEqual(first, second) || first.Checks[0].State != StateIssueObserved {
		t.Fatalf("replay/input order changed the result: %v", err)
	}
	// A genuinely newer success is recovery for this check, not a claim that
	// every check throughout the history window succeeded.
	newer := snapshot.Measurements[1]
	newer.ID, newer.Outcome, newer.Successes = "recovery", OutcomeSucceeded, 4
	newer.CompletedAt = snapshot.AsOf
	snapshot.Measurements = append(snapshot.Measurements, newer)
	recovered, err := Assess(snapshot)
	if err != nil || recovered.Checks[0].EvidenceID != "recovery" || recovered.Checks[0].State != StateSucceeded {
		t.Fatalf("fresh evidence did not recover the check: %v", err)
	}
}

func TestFamiliesAndTargetsRemainIndependent(t *testing.T) {
	snapshot := fixture(gateway(), OutcomeFailed)
	v6 := Target{"external-v6", LayerExternal, MethodHTTPS, FamilyIPv6}
	unmeasured := Target{"resolver-v4", LayerDNS, MethodDNS, FamilyIPv4}
	snapshot.Targets = append(snapshot.Targets, v6, unmeasured)
	success := snapshot.Measurements[0]
	success.ID, success.TargetID, success.Outcome, success.Successes = "v6-success", v6.ID, OutcomeSucceeded, 4
	snapshot.Measurements = append(snapshot.Measurements, success)
	report, err := Assess(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if report.Checks[0].State != StateIssueObserved || report.Checks[1].State != StateSucceeded ||
		report.Checks[2].State != StateUnknown || report.Checks[2].EvidenceID != "" {
		t.Fatalf("one result leaked into other families/targets: %+v", report)
	}
}

func TestMissingEvidenceAndEmptyConfigurationStayUnknown(t *testing.T) {
	snapshot := fixture(gateway(), OutcomeSucceeded)
	snapshot.Measurements = nil
	report, err := Assess(snapshot)
	if err != nil || report.Checks[0].State != StateUnknown || report.Checks[0].Gap != GapNone ||
		report.Checks[0].ReplyLossPercent != nil || report.Checks[0].MeanLatency != nil || !report.Checks[0].CompletedAt.IsZero() {
		t.Fatalf("missing data invented a measurement: %+v, %v", report, err)
	}
	snapshot.Targets = nil
	report, err = Assess(snapshot)
	if err != nil || len(report.Checks) != 0 || len(report.Limitations) == 0 {
		t.Fatalf("empty selection implied health: %+v, %v", report, err)
	}
}

func TestAssessmentDoesNotAliasOrMutateInput(t *testing.T) {
	snapshot := fixture(gateway(), OutcomeSucceeded)
	latency := 20 * time.Millisecond
	snapshot.Measurements[0].MeanLatency = &latency
	before := snapshot.Measurements[0]
	report, err := Assess(snapshot)
	if err != nil || !reflect.DeepEqual(before, snapshot.Measurements[0]) {
		t.Fatalf("assessment mutated input: %v", err)
	}
	latency = time.Second
	if *report.Checks[0].MeanLatency != 20*time.Millisecond {
		t.Fatal("report aliases caller latency")
	}
	*report.Checks[0].MeanLatency = 50 * time.Millisecond
	if *snapshot.Measurements[0].MeanLatency != time.Second {
		t.Fatal("caller aliases report latency")
	}
}
