package gatewayrun

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func historicalEvents(t *testing.T, replies int) ([]Event, time.Time) {
	t.Helper()
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	base := Event{SchemaVersion: 2, RunID: strings.Repeat("a", 32), ScopeID: "scope.home", Profile: Profile,
		SelectionDigest: strings.Repeat("b", 64), InterfaceName: "en0", InterfaceIndex: 7, Source: "192.168.50.23", Target: "192.168.50.1"}
	auth := base
	auth.State, auth.At = "authorized", at
	admit := base
	admit.State, admit.At = "admitted", at.Add(time.Millisecond)
	terminal := base
	terminal.State, terminal.Outcome, terminal.At = "finished", "completed", at.Add(4*time.Second)
	end := at.Add(3 * time.Second)
	terminal.Measurement = &Measurement{StartedAt: admit.At, CompletedAt: &end, SendCalls: 3, AcceptedRequests: 3, Replies: replies, Timeouts: 3 - replies, Complete: true}
	// Three timeouts require a full three seconds after sample start.
	end = admit.At.Add(3 * time.Second)
	if replies > 0 {
		ns := int64(0)
		terminal.Measurement.MeanRTTNanoseconds = &ns
	}
	return []Event{auth, admit, terminal}, at.Add(10 * time.Second)
}

func TestRetainedAssessmentIsHistoricalAndOrderIndependent(t *testing.T) {
	for replies := 0; replies <= 3; replies++ {
		events, asOf := historicalEvents(t, replies)
		run, err := DescribeRetainedRun(events, asOf)
		if err != nil || run.Outcome != "completed" || run.Measurement == nil || run.Assessment.Confidence != "limited" ||
			!run.AuthorizationRetained || !run.AdmissionRetained || !run.TerminalRetained || *run.Assessment.ReplyLossPercent != 100*float64(3-replies)/3 {
			t.Fatalf("invalid history: %+v %v", run, err)
		}
		want := "some-replies"
		if replies == 0 {
			want = "no-replies"
		}
		if replies == 3 {
			want = "all-replied"
		}
		if run.Assessment.State != want || (run.Measurement.MeanRTTNanoseconds == nil) != (replies == 0) {
			t.Fatal("wrong sample assessment")
		}
		events[0], events[2] = events[2], events[0]
		again, err := DescribeRetainedRun(events, asOf.Add(24*time.Hour))
		if err != nil || !reflect.DeepEqual(run, again) {
			t.Fatal("read time or row order refreshed/changed history", err)
		}
	}
}

func TestMissingLegacyAndIncompleteHistoryRemainUnknown(t *testing.T) {
	events, asOf := historicalEvents(t, 3)
	for _, input := range [][]Event{events[:1], events[1:2], events[:2]} {
		r, err := DescribeRetainedRun(input, asOf)
		if err != nil || r.TerminalRetained || r.Outcome != "unknown" || r.Measurement != nil || r.Assessment.ReplyLossPercent != nil || r.Assessment.State != "unknown" {
			t.Fatal("missing terminal invented outcome", err)
		}
	}
	legacy := events[2]
	legacy.SchemaVersion = 1
	legacy.Measurement = nil
	r, err := DescribeRetainedRun([]Event{legacy}, asOf)
	if err != nil || r.Outcome != "completed" || r.Measurement != nil || r.Assessment.State != "unknown" {
		t.Fatal("legacy upgraded into measurement", err)
	}
	partial := events[2]
	partial.Outcome, partial.Reason = "failed", "execution-error"
	partial.Measurement = &Measurement{StartedAt: events[1].At, SendCalls: 1, AcceptedRequests: 1}
	r, err = DescribeRetainedRun([]Event{partial}, asOf)
	if err != nil || r.Assessment.State != "incomplete" || r.Assessment.ReplyLossPercent != nil || r.Measurement.MeanRTTNanoseconds != nil {
		t.Fatal("partial invented metrics", err)
	}
	// A terminal record remains useful when its earlier phases have expired.
	r, err = DescribeRetainedRun(events[2:], asOf)
	if err != nil || r.AuthorizationRetained || r.AdmissionRetained || !r.TerminalRetained || r.Measurement == nil {
		t.Fatal("missing phases fabricated/discarded", err)
	}
}

func TestContradictoryHistoryFailsWithoutLeakingEvidence(t *testing.T) {
	for name, mutate := range map[string]func([]Event) []Event{
		"duplicate":                    func(e []Event) []Event { return append(e[:2], e[0]) },
		"scope":                        func(e []Event) []Event { e[2].ScopeID = "scope.other"; return e },
		"digest":                       func(e []Event) []Event { e[2].SelectionDigest = strings.Repeat("c", 64); return e },
		"source":                       func(e []Event) []Event { e[2].Source = "192.168.50.24"; return e },
		"version":                      func(e []Event) []Event { e[0].SchemaVersion = 1; return e },
		"chronology":                   func(e []Event) []Event { e[1].At = e[0].At.Add(-time.Second); return e },
		"measurement-before-admission": func(e []Event) []Event { e[1].At = e[2].At; return e },
		"future":                       func(e []Event) []Event { e[2].At = e[2].At.Add(time.Hour); return e },
		"empty":                        func(e []Event) []Event { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			e, asOf := historicalEvents(t, 3)
			r, err := DescribeRetainedRun(mutate(e), asOf)
			if err != ErrHistory || !reflect.DeepEqual(r, RetainedRun{}) {
				t.Fatalf("published contradictory history: %+v %v", r, err)
			}
		})
	}
}

func TestRetainedEventDecoderAndPointerIsolation(t *testing.T) {
	e, asOf := historicalEvents(t, 3)
	raw, _ := json.Marshal(e[2])
	got, err := DecodeRetainedEvent(raw)
	if err != nil || !reflect.DeepEqual(got, e[2]) {
		t.Fatal("valid stored event rejected", err)
	}
	for _, malformed := range []string{
		strings.Replace(string(raw), `"state":`, `"state":"admitted","state":`, 1),
		strings.Replace(string(raw), `"state":`, `"State":`, 1),
		strings.Replace(string(raw), `"replies":3`, `"replies":0,"\u0072eplies":3`, 1),
		strings.Replace(string(raw), `"complete":true`, `"Complete":true`, 1),
		strings.Replace(string(raw), `"send_calls":3,`, ``, 1),
		strings.Replace(string(raw), `"schema_version":2`, `"schema_version":null`, 1),
		string(raw) + ` true`, `null`, strings.Repeat(" ", MaxRetainedEventBytes) + string(raw),
	} {
		if _, err := DecodeRetainedEvent([]byte(malformed)); err != ErrHistory {
			t.Fatal("ambiguous event accepted", err)
		}
	}
	r, err := DescribeRetainedRun(e, asOf)
	if err != nil {
		t.Fatal(err)
	}
	*r.Measurement.CompletedAt = asOf
	*r.Measurement.MeanRTTNanoseconds = 123
	if e[2].Measurement.CompletedAt.Equal(asOf) || *e[2].Measurement.MeanRTTNanoseconds != 0 {
		t.Fatal("history aliases retained evidence")
	}
}

func FuzzRetainedGatewayEvent(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema_version":2,"schema_version":1}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if e, err := DecodeRetainedEvent(raw); err == nil && (len(raw) > MaxRetainedEventBytes || ValidateEvent(e) != nil) {
			t.Fatal("invalid retained event")
		}
	})
}
