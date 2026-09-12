package httpsrun

import (
	"encoding/json"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"reflect"
	"strings"
	"testing"
	"time"
)

func httpsHistoryEvents() ([]Event, time.Time) {
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	base := Event{SchemaVersion: 1, RunID: strings.Repeat("a", 32), Profile: Profile, Selection: nq.HTTPSSelection{ID: "selection-v1", EndpointID: "endpoint-v1", RequestID: "request-v1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Observer: nq.Observer{ScopeID: "scope.home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}}
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
	terminal.Measurement = &Measurement{StartedAt: admit.At, CompletedAt: at.Add(time.Second), Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTimeNanoseconds: &zero}
	return []Event{auth, admit, terminal}, at.Add(time.Minute)
}
func TestHTTPSHistoryIsHistoricalOrderIndependentAndOwned(t *testing.T) {
	events, now := httpsHistoryEvents()
	r, err := DescribeRetainedRun(events, now)
	if err != nil || r.Assessment.State != "status-response" || r.Assessment.ExpectationMatched == nil || *r.Assessment.ExpectationMatched || r.Outcome != "completed" || r.Measurement.ResponseTimeNanoseconds == nil || *r.Measurement.ResponseTimeNanoseconds != 0 {
		t.Fatal("HTTPS error/timing changed", err)
	}
	events[0], events[2] = events[2], events[0]
	later, err := DescribeRetainedRun(events, now.Add(10*24*time.Hour))
	if err != nil || !reflect.DeepEqual(r, later) {
		t.Fatal("read time or row order changed history", err)
	}
	r.Measurement.StatusCode = 0
	*r.Measurement.ResponseTimeNanoseconds = 10
	if events[0].Measurement.StatusCode != 503 || *events[0].Measurement.ResponseTimeNanoseconds != 0 {
		t.Fatal("returned sample aliases stored event")
	}
}
func TestHTTPSHistoryMissingPhasesAndPartialEvidence(t *testing.T) {
	events, now := httpsHistoryEvents()
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
	partial.Measurement = &Measurement{StartedAt: events[1].At, CompletedAt: events[2].At, Request: nq.HTTPSRequestUncertain, Exchange: nq.HTTPSIncomplete, Stage: nq.HTTPSRequest}
	r, err = DescribeRetainedRun([]Event{partial}, now)
	if err != nil || r.Assessment.State != "incomplete" || r.Assessment.Confidence != "unknown" || r.Measurement.StatusCode != 0 || r.Assessment.ExpectationMatched != nil {
		t.Fatal("partial exchange became timeout/success", err)
	}
}
func TestHTTPSHistoryRejectsConflictsAndMalformedJSON(t *testing.T) {
	events, now := httpsHistoryEvents()
	for _, change := range []func(*Event){func(e *Event) { e.Selection.RequestID = "other" }, func(e *Event) { e.Observer.SensorID = "other" }, func(e *Event) { e.At = events[0].At.Add(-time.Second) }, func(e *Event) { e.RunID = strings.Repeat("b", 32) }} {
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
	for _, bad := range []string{strings.Replace(string(raw), `"state":`, `"state":"authorized","state":`, 1), strings.Replace(string(raw), `"EndpointID":`, `"endpointid":`, 1), strings.Replace(string(raw), `"status_code":503`, `"status_code":503,"status_code":204`, 1), strings.Replace(string(raw), `"response_time_ns":0`, `"response_time_ns":null`, 1), strings.Replace(string(raw), `"measurement":`, `"private":"do-not-echo","measurement":`, 1), string(raw) + " true", strings.Repeat("x", 4097)} {
		if _, err := DecodeRetainedEvent([]byte(bad)); err == nil || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatal("invalid history decoded or leaked", err)
		}
	}
}
func TestHTTPSHistoryPreservesResponseStatusAndExpectation(t *testing.T) {
	for _, pair := range [][2]int{{204, 204}, {301, 204}, {404, 204}, {503, 204}, {301, 301}, {503, 503}} {
		code, expected := pair[0], pair[1]
		events, now := httpsHistoryEvents()
		for i := range events {
			events[i].Selection.ExpectedStatus = expected
		}
		events[2].Measurement.StatusCode = code
		r, err := DescribeRetainedRun(events, now)
		if err != nil || r.Assessment.ExpectationMatched == nil || *r.Assessment.ExpectationMatched != (code == expected) || r.Measurement.StatusCode != code {
			t.Fatal("lost HTTP status semantics", err)
		}
		want := "status-response"
		if code == 301 {
			want = "redirect-response"
		}
		if r.Assessment.State != want {
			t.Fatal(r.Assessment)
		}
	}
}

func TestHTTPSHistoryDistinguishesFailuresFromTimeouts(t *testing.T) {
	for _, tc := range []struct {
		exchange nq.HTTPSExchange
		stage    nq.HTTPSStage
		request  nq.HTTPSRequestState
		state    string
	}{
		{nq.HTTPSConnectError, nq.HTTPSConnect, nq.HTTPSRequestNotSent, "connection-failure"},
		{nq.HTTPSTLSError, nq.HTTPSTLS, nq.HTTPSRequestNotSent, "tls-failure"},
		{nq.HTTPSProtocolError, nq.HTTPSRequest, nq.HTTPSRequestAccepted, "protocol-failure"},
		{nq.HTTPSTransportError, nq.HTTPSRequest, nq.HTTPSRequestUncertain, "transport-failure"},
		{nq.HTTPSIncomplete, nq.HTTPSRequest, nq.HTTPSRequestUncertain, "incomplete"},
		{nq.HTTPSTimeout, nq.HTTPSRequest, nq.HTTPSRequestAccepted, "timeout"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			events, now := httpsHistoryEvents()
			e := &events[2]
			e.Outcome, e.Reason = "failed", "execution-error"
			if tc.exchange == nq.HTTPSTimeout {
				e.Outcome, e.Reason = "completed", ""
			}
			e.Measurement = &Measurement{StartedAt: events[1].At, CompletedAt: e.At, Exchange: tc.exchange, Stage: tc.stage, Request: tc.request}
			r, err := DescribeRetainedRun(events, now)
			if err != nil || r.Assessment.State != tc.state || r.Assessment.ExpectationMatched != nil || r.Measurement.ResponseTimeNanoseconds != nil {
				t.Fatal("failure turned into response/loss", err)
			}
		})
	}
}
