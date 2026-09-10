package networkquality

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInvalidSnapshotsFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"missing observer", func(s *Snapshot) { s.Observer = Observer{} }},
		{"hostile scope", func(s *Snapshot) { s.Observer.ScopeID = "<script>" }},
		{"invalid sensor", func(s *Snapshot) { s.Observer.SensorID = "SENSOR" }},
		{"invalid interface", func(s *Snapshot) { s.Observer.InterfaceName = "en0;id" }},
		{"zero interface index", func(s *Snapshot) { s.Observer.InterfaceIndex = 0 }},
		{"missing as-of", func(s *Snapshot) { s.AsOf = time.Time{} }},
		{"missing window", func(s *Snapshot) { s.WindowStart = time.Time{} }},
		{"inverted window", func(s *Snapshot) { s.WindowStart = s.AsOf.Add(time.Second) }},
		{"oversized window", func(s *Snapshot) { s.WindowStart = s.AsOf.Add(-MaxWindow - time.Nanosecond) }},
		{"zero freshness", func(s *Snapshot) { s.Freshness = 0 }},
		{"negative freshness", func(s *Snapshot) { s.Freshness = -time.Second }},
		{"excess freshness", func(s *Snapshot) { s.Freshness = MaxFreshness + time.Nanosecond }},
		{"oversized targets", func(s *Snapshot) { s.Targets = make([]Target, MaxTargets+1) }},
		{"oversized measurements", func(s *Snapshot) { s.Measurements = make([]Measurement, MaxMeasurements+1) }},
		{"duplicate target", func(s *Snapshot) { s.Targets = append(s.Targets, s.Targets[0]) }},
		{"invalid target id", func(s *Snapshot) { s.Targets[0].ID = "https://secret@example.test" }},
		{"oversized target id", func(s *Snapshot) { s.Targets[0].ID = strings.Repeat("a", 129) }},
		{"invalid layer", func(s *Snapshot) { s.Targets[0].Layer = "security" }},
		{"arbitrary method", func(s *Snapshot) { s.Targets[0].Method = "shell" }},
		{"wrong layer method", func(s *Snapshot) { s.Targets[0].Method = MethodDNS }},
		{"missing family", func(s *Snapshot) { s.Targets[0].Family = FamilyNone }},
		{"invented family", func(s *Snapshot) { s.Targets[0].Family = "dual-stack" }},
		{"unknown target", func(s *Snapshot) { s.Measurements[0].TargetID = "other" }},
		{"wrong scope", func(s *Snapshot) { s.Measurements[0].Observer.ScopeID = "other-network" }},
		{"wrong sensor", func(s *Snapshot) { s.Measurements[0].Observer.SensorID = "other-sensor" }},
		{"wrong interface", func(s *Snapshot) { s.Measurements[0].Observer.InterfaceName = "utun0" }},
		{"wrong interface index", func(s *Snapshot) { s.Measurements[0].Observer.InterfaceIndex = 2 }},
		{"empty evidence id", func(s *Snapshot) { s.Measurements[0].ID = "" }},
		{"hostile evidence id", func(s *Snapshot) { s.Measurements[0].ID = "secret\nvalue" }},
		{"duplicate evidence", func(s *Snapshot) { s.Measurements = append(s.Measurements, s.Measurements[0]) }},
		{"tied evidence", func(s *Snapshot) {
			other := s.Measurements[0]
			other.ID, other.Outcome, other.Successes = "contradiction", OutcomeFailed, 0
			other.CompletedAt = other.CompletedAt.In(time.FixedZone("equivalent", 3600))
			s.Measurements = append(s.Measurements, other)
		}},
		{"missing start", func(s *Snapshot) { s.Measurements[0].StartedAt = time.Time{} }},
		{"missing completion", func(s *Snapshot) { s.Measurements[0].CompletedAt = time.Time{} }},
		{"future evidence", func(s *Snapshot) { s.Measurements[0].CompletedAt = s.AsOf.Add(time.Second) }},
		{"out of window", func(s *Snapshot) { s.Measurements[0].StartedAt = s.WindowStart.Add(-time.Second) }},
		{"negative duration", func(s *Snapshot) { s.Measurements[0].StartedAt = s.AsOf }},
		{"excess check time", func(s *Snapshot) {
			s.Measurements[0].StartedAt = s.Measurements[0].CompletedAt.Add(-MaxCheckTime - time.Nanosecond)
		}},
		{"invented outcome", func(s *Snapshot) { s.Measurements[0].Outcome = "internet-down" }},
		{"gap on measured result", func(s *Snapshot) { s.Measurements[0].Gap = GapSleep }},
		{"zero attempts", func(s *Snapshot) { s.Measurements[0].Attempts = 0 }},
		{"negative attempts", func(s *Snapshot) { s.Measurements[0].Attempts = -1 }},
		{"excess attempts", func(s *Snapshot) { s.Measurements[0].Attempts = MaxAttempts + 1 }},
		{"negative successes", func(s *Snapshot) { s.Measurements[0].Successes = -1 }},
		{"excess successes", func(s *Snapshot) { s.Measurements[0].Successes = 5 }},
		{"success with losses", func(s *Snapshot) { s.Measurements[0].Successes = 3 }},
		{"failed with successes", func(s *Snapshot) { s.Measurements[0].Outcome = OutcomeFailed }},
		{"partial with all successes", func(s *Snapshot) { s.Measurements[0].Outcome = OutcomePartial }},
		{"partial with no successes", func(s *Snapshot) { s.Measurements[0].Outcome, s.Measurements[0].Successes = OutcomePartial, 0 }},
		{"negative latency", func(s *Snapshot) { v := -time.Second; s.Measurements[0].MeanLatency = &v }},
		{"impossible latency", func(s *Snapshot) { v := time.Hour; s.Measurements[0].MeanLatency = &v }},
		{"latency without success", func(s *Snapshot) {
			v := time.Millisecond
			s.Measurements[0].MeanLatency = &v
			s.Measurements[0].Outcome, s.Measurements[0].Successes = OutcomeFailed, 0
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := fixture(gateway(), OutcomeSucceeded)
			tc.mutate(&snapshot)
			report, err := Assess(snapshot)
			if err == nil || !reflect.DeepEqual(report, Report{}) {
				t.Fatalf("invalid input produced an assessment: %+v, %v", report, err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "<script>") {
				t.Fatal("untrusted input leaked into diagnostic")
			}
		})
	}
}

func TestGapValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Measurement)
	}{
		{"missing reason", func(m *Measurement) { m.Gap = GapNone }},
		{"invented reason", func(m *Measurement) { m.Gap = "isp-outage" }},
		{"unavailable is not sleep", func(m *Measurement) { m.Gap = GapSleep }},
		{"not-run is not denied", func(m *Measurement) { m.Outcome = OutcomeNotRun }},
		{"unavailable with attempts", func(m *Measurement) { m.Attempts = 1 }},
		{"unavailable with successes", func(m *Measurement) { m.Successes = 1 }},
		{"unavailable with latency", func(m *Measurement) { v := time.Duration(0); m.MeanLatency = &v }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := fixture(gateway(), OutcomeFailed)
			m := &snapshot.Measurements[0]
			m.Outcome, m.Gap, m.Attempts = OutcomeUnavailable, GapPermission, 0
			tc.mutate(m)
			if err := ValidateSnapshot(snapshot); err == nil {
				t.Fatal("invalid gap accepted")
			}
		})
	}
}

func TestLocalInterfaceRejectsProbeSemantics(t *testing.T) {
	for _, change := range []func(*Snapshot){
		func(s *Snapshot) { s.Targets[0].Method = MethodICMP },
		func(s *Snapshot) { s.Targets[0].Family = FamilyIPv4 },
		func(s *Snapshot) { s.Measurements[0].Outcome = OutcomePartial },
		func(s *Snapshot) { s.Measurements[0].Attempts = 1 },
		func(s *Snapshot) { s.Measurements[0].Successes = 1 },
		func(s *Snapshot) { v := time.Duration(0); s.Measurements[0].MeanLatency = &v },
	} {
		snapshot := fixture(Target{"link", LayerLink, MethodInterface, FamilyNone}, OutcomeSucceeded)
		change(&snapshot)
		if err := ValidateSnapshot(snapshot); err == nil {
			t.Fatal("interface-state acquired probe semantics")
		}
	}
}

func TestBoundsAreInclusiveAndMetricsRemainOptional(t *testing.T) {
	snapshot := fixture(gateway(), OutcomeSucceeded)
	snapshot.WindowStart = snapshot.AsOf.Add(-MaxWindow)
	snapshot.Freshness = MaxFreshness
	snapshot.Measurements[0].Attempts, snapshot.Measurements[0].Successes = MaxAttempts, MaxAttempts
	snapshot.Measurements[0].StartedAt = snapshot.Measurements[0].CompletedAt.Add(-MaxCheckTime)
	zero := time.Duration(0)
	snapshot.Measurements[0].MeanLatency = &zero
	for i := 1; i < MaxTargets; i++ {
		target := gateway()
		target.ID = fmt.Sprintf("target-%d", i)
		snapshot.Targets = append(snapshot.Targets, target)
	}
	for i := 1; i < MaxMeasurements; i++ {
		m := snapshot.Measurements[0]
		m.ID = fmt.Sprintf("measurement-%d", i+1)
		m.StartedAt, m.CompletedAt = m.StartedAt.Add(-time.Duration(i)*time.Minute), m.CompletedAt.Add(-time.Duration(i)*time.Minute)
		snapshot.Measurements = append(snapshot.Measurements, m)
	}
	report, err := Assess(snapshot)
	if err != nil || len(report.Checks) != MaxTargets || report.Checks[0].MeanLatency == nil || *report.Checks[0].MeanLatency != 0 {
		t.Fatalf("valid boundary or explicit zero rejected: %v", err)
	}
	snapshot.Freshness = time.Second
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
}
