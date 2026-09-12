package resolverrun

import (
	"encoding/json"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"reflect"
	"strings"
	"testing"
	"time"
)

func dnsHistoryEvents() ([]Event, time.Time) {
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	base := Event{SchemaVersion: 1, RunID: strings.Repeat("a", 32), Profile: Profile, Selection: nq.ResolverSelection{ID: "selection-v1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Observer: nq.Observer{ScopeID: "scope.home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}}
	auth := base
	auth.State = "authorized"
	auth.At = at
	admit := base
	admit.State = "admitted"
	admit.At = at.Add(time.Millisecond)
	terminal := base
	terminal.State = "finished"
	terminal.Outcome = "completed"
	terminal.At = at.Add(3 * time.Second)
	zero := int64(0)
	terminal.Measurement = &Measurement{StartedAt: admit.At, CompletedAt: at.Add(time.Second), Request: nq.DNSRequestAccepted, Exchange: nq.DNSResponseReceived, Reply: &nq.DNSReply{RCode: 3}, ResponseTimeNanoseconds: &zero}
	return []Event{auth, admit, terminal}, at.Add(time.Minute)
}
func TestResolverHistoryIsHistoricalOrderIndependentAndOwned(t *testing.T) {
	events, now := dnsHistoryEvents()
	r, err := DescribeRetainedRun(events, now)
	if err != nil || r.Assessment.State != "nxdomain" || r.Assessment.ExpectationMatched == nil || *r.Assessment.ExpectationMatched || r.Outcome != "completed" || r.Measurement.ResponseTimeNanoseconds == nil || *r.Measurement.ResponseTimeNanoseconds != 0 {
		t.Fatal("DNS error/timing changed", err)
	}
	events[0], events[2] = events[2], events[0]
	later, err := DescribeRetainedRun(events, now.Add(10*24*time.Hour))
	if err != nil || !reflect.DeepEqual(r, later) {
		t.Fatal("read time or row order changed history", err)
	}
	r.Measurement.Reply.RCode = 0
	*r.Measurement.ResponseTimeNanoseconds = 10
	if events[0].Measurement.Reply.RCode != 3 || *events[0].Measurement.ResponseTimeNanoseconds != 0 {
		t.Fatal("returned sample aliases stored event")
	}
}
func TestResolverHistoryMissingPhasesAndPartialEvidence(t *testing.T) {
	events, now := dnsHistoryEvents()
	for _, subset := range [][]Event{events[:1], events[1:2], events[:2]} {
		r, err := DescribeRetainedRun(subset, now)
		if err != nil || r.Outcome != "unknown" || r.Measurement != nil || r.TerminalRetained {
			t.Fatal("missing terminal invented result", err)
		}
	}
	r, err := DescribeRetainedRun(events[2:], now)
	if err != nil || r.AuthorizationRetained || r.AdmissionRetained || !r.TerminalRetained || r.Measurement == nil {
		t.Fatal("missing siblings discarded terminal", err)
	}
	partial := events[2]
	partial.Outcome = "failed"
	partial.Reason = "execution-error"
	partial.Measurement = &Measurement{StartedAt: events[1].At, CompletedAt: events[2].At, Request: nq.DNSRequestUncertain, Exchange: nq.DNSIncomplete}
	r, err = DescribeRetainedRun([]Event{partial}, now)
	if err != nil || r.Assessment.State != "incomplete" || r.Assessment.Confidence != "unknown" || r.Measurement.Reply != nil || r.Assessment.ExpectationMatched != nil {
		t.Fatal("partial exchange became timeout/success", err)
	}
}
func TestResolverHistoryRejectsConflictsAndMalformedJSON(t *testing.T) {
	events, now := dnsHistoryEvents()
	for _, change := range []func(*Event){func(e *Event) { e.Selection.QueryID = "other" }, func(e *Event) { e.Observer.SensorID = "other" }, func(e *Event) { e.At = events[0].At.Add(-time.Second) }, func(e *Event) { e.RunID = strings.Repeat("b", 32) }} {
		copy := append([]Event(nil), events...)
		change(&copy[1])
		if _, err := DescribeRetainedRun(copy, now); err == nil {
			t.Fatal("conflicting siblings accepted")
		}
	}
	raw, _ := json.Marshal(events[2])
	decoded, err := DecodeRetainedEvent(raw)
	if err != nil || !reflect.DeepEqual(decoded, events[2]) {
		t.Fatal("valid persisted event rejected", err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"state":`, `"state":"authorized","state":`, 1), strings.Replace(string(raw), `"ResolverID":`, `"resolverid":`, 1), strings.Replace(string(raw), `"RCode":3`, `"RCode":3,"RCode":0`, 1), strings.Replace(string(raw), `"response_time_ns":0`, `"response_time_ns":null`, 1), strings.Replace(string(raw), `"measurement":`, `"private":"do-not-echo","measurement":`, 1), string(raw) + " true", strings.Repeat("x", 4097)} {
		if _, err := DecodeRetainedEvent([]byte(bad)); err == nil || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatal("invalid history decoded or leaked", err)
		}
	}
}
func TestResolverHistoryPreservesDistinctDNSResults(t *testing.T) {
	for _, tc := range []struct {
		code      int
		answer    nq.DNSAnswerKind
		truncated bool
		want      string
	}{{0, nq.DNSAnswerPresent, false, "answer"}, {0, nq.DNSAnswerNoData, false, "no-data"}, {0, nq.DNSAnswerReferral, false, "referral"}, {0, nq.DNSAnswerUnclassified, false, "unclassified-response"}, {0, "", true, "truncated"}, {1, "", false, "format-error"}, {2, "", false, "server-failure"}, {3, "", false, "nxdomain"}, {4, "", false, "not-implemented"}, {5, "", false, "refused"}, {15, "", false, "other-response-error"}} {
		events, now := dnsHistoryEvents()
		events[2].Measurement.Reply = &nq.DNSReply{RCode: tc.code, Answer: tc.answer, Truncated: tc.truncated}
		r, err := DescribeRetainedRun(events, now)
		if err != nil || r.Assessment.State != tc.want {
			t.Fatal("DNS result collapsed", tc.want, err)
		}
	}
	events, now := dnsHistoryEvents()
	m := events[2].Measurement
	m.Exchange = nq.DNSTimeout
	m.Reply = nil
	m.ResponseTimeNanoseconds = nil
	m.CompletedAt = m.StartedAt.Add(2 * time.Second)
	r, err := DescribeRetainedRun(events, now)
	if err != nil || r.Assessment.State != "timeout" {
		t.Fatal("accepted timeout lost", err)
	}
}
