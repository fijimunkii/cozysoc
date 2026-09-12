package networkquality

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func httpsFixture() HTTPSSnapshot {
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	o := Observer{ScopeID: "scope.home", SensorID: "sensor.https", InterfaceName: "en0", InterfaceIndex: 7}
	s := HTTPSSelection{ID: "selection.https", EndpointID: "endpoint.test", RequestID: "request.test", Family: FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}
	elapsed := time.Second
	return HTTPSSnapshot{Observer: o, AsOf: at, WindowStart: at.Add(-time.Hour), Freshness: time.Minute, Selections: []HTTPSSelection{s},
		Measurements: []HTTPSMeasurement{{ID: "measurement.https", Selection: s, Observer: o, StartedAt: at.Add(-3 * time.Second), CompletedAt: at.Add(-time.Second), Stage: HTTPSRequest, Exchange: HTTPSResponseReceived, Request: HTTPSRequestAccepted, StatusCode: 204, ResponseTime: &elapsed}}}
}
func TestHTTPSFinalStatusesPreserveResponseAndExpectation(t *testing.T) {
	for _, status := range []int{200, 204, 299, 300, 301, 302, 303, 304, 307, 308, 399, 400, 404, 407, 429, 500, 503, 511, 599} {
		for _, expected := range []int{204, status} {
			s := httpsFixture()
			s.Selections[0].ExpectedStatus = expected
			m := &s.Measurements[0]
			m.Selection = s.Selections[0]
			m.StatusCode = status
			out, err := AssessHTTPS(s)
			if err != nil {
				t.Fatal(err)
			}
			c := out.Checks[0]
			want := StateIssueObserved
			if expected == status {
				want = StateSucceeded
			}
			if c.State != want || c.Confidence != ConfidenceLimited || c.ExpectationMatched == nil || *c.ExpectationMatched != (expected == status) || c.Evidence.StatusCode != status || c.ResponseTime == nil {
				t.Fatalf("status %d expected %d: %+v", status, expected, c)
			}
			result := HTTPSStatusResponse
			switch status {
			case 301, 302, 303, 307, 308:
				result = HTTPSRedirectResponse
			}
			if c.Result != result {
				t.Fatalf("status %d misclassified: %s", status, c.Result)
			}
		}
	}
}
func TestHTTPSStageAndFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		exchange HTTPSExchange
		stage    HTTPSStage
		request  HTTPSRequestState
		result   HTTPSResult
		state    State
	}{
		{HTTPSConnectError, HTTPSConnect, HTTPSRequestNotSent, HTTPSConnectionFailure, StateIssueObserved},
		{HTTPSTransportError, HTTPSTLS, HTTPSRequestNotSent, HTTPSTransportFailure, StateIssueObserved},
		{HTTPSTransportError, HTTPSRequest, HTTPSRequestAccepted, HTTPSTransportFailure, StateIssueObserved},
		{HTTPSTransportError, HTTPSRequest, HTTPSRequestUncertain, HTTPSTransportFailure, StateIssueObserved},
		{HTTPSTLSError, HTTPSTLS, HTTPSRequestNotSent, HTTPSTLSFailure, StateIssueObserved},
		{HTTPSProtocolError, HTTPSRequest, HTTPSRequestAccepted, HTTPSProtocolFailure, StateIssueObserved},
		{HTTPSTimeout, HTTPSConnect, HTTPSRequestNotSent, HTTPSTimedOut, StateIssueObserved},
		{HTTPSTimeout, HTTPSTLS, HTTPSRequestNotSent, HTTPSTimedOut, StateIssueObserved},
		{HTTPSTimeout, HTTPSRequest, HTTPSRequestUncertain, HTTPSTimedOut, StateIssueObserved},
		{HTTPSTimeout, HTTPSRequest, HTTPSRequestAccepted, HTTPSTimedOut, StateIssueObserved},
		{HTTPSIncomplete, HTTPSConnect, HTTPSRequestNotSent, HTTPSUnfinished, StateUnknown},
		{HTTPSIncomplete, HTTPSTLS, HTTPSRequestNotSent, HTTPSUnfinished, StateUnknown},
		{HTTPSIncomplete, HTTPSRequest, HTTPSRequestUncertain, HTTPSUnfinished, StateUnknown},
		{HTTPSIncomplete, HTTPSRequest, HTTPSRequestAccepted, HTTPSUnfinished, StateUnknown},
	} {
		s := httpsFixture()
		m := &s.Measurements[0]
		m.Exchange, m.Stage, m.Request = tc.exchange, tc.stage, tc.request
		m.StatusCode = 0
		m.ResponseTime = nil
		out, err := AssessHTTPS(s)
		if err != nil {
			t.Fatal(err)
		}
		c := out.Checks[0]
		if c.State != tc.state || c.Result != tc.result || c.ExpectationMatched != nil || c.ResponseTime != nil {
			t.Fatalf("%+v: %+v", tc, c)
		}
	}
}
func TestHTTPSLatestGapsAndUnfinishedWorkSupersedeSuccess(t *testing.T) {
	for _, gap := range []GapReason{GapPermission, GapUnsupported, GapSourceUnavailable, GapDisabled, GapNotConfigured, GapSleep, GapOffline, GapNetworkChanged, GapNone} {
		s := httpsFixture()
		m := s.Measurements[0]
		m.ID = "newer"
		m.StartedAt = s.AsOf
		m.CompletedAt = s.AsOf
		m.ResponseTime = nil
		m.StatusCode = 0
		m.Request = HTTPSRequestNotSent
		m.Stage = ""
		m.Gap = gap
		m.Exchange = HTTPSNotMeasured
		state := StateNotMeasured
		if gap == GapNone {
			m.Exchange = HTTPSIncomplete
			m.Stage = HTTPSConnect
			state = StateUnknown
		}
		s.Measurements = append(s.Measurements, m)
		before, err := AssessHTTPS(s)
		if err != nil {
			t.Fatal(err)
		}
		s.Measurements[0], s.Measurements[1] = s.Measurements[1], s.Measurements[0]
		after, err := AssessHTTPS(s)
		if err != nil || !reflect.DeepEqual(before, after) || after.Checks[0].State != state || after.Checks[0].Evidence.ID != "newer" {
			t.Fatalf("gap %s: %+v %v", gap, after, err)
		}
	}
}
func TestHTTPSFreshnessOwnershipAndMissingEvidence(t *testing.T) {
	s := httpsFixture()
	out, err := AssessHTTPS(s)
	if err != nil {
		t.Fatal(err)
	}
	*s.Measurements[0].ResponseTime = 0
	s.Selections[0].ExpectedStatus = 500
	if *out.Checks[0].ResponseTime != time.Second || *out.Checks[0].Evidence.ResponseTime != time.Second || out.Checks[0].Selection.ExpectedStatus != 204 {
		t.Fatal("input aliases report")
	}
	*out.Checks[0].ResponseTime = 0
	if *out.Checks[0].Evidence.ResponseTime != time.Second {
		t.Fatal("fresh metric aliases historical evidence")
	}
	s = httpsFixture()
	s.AsOf = s.Measurements[0].CompletedAt.Add(s.Freshness)
	out, err = AssessHTTPS(s)
	if err != nil {
		t.Fatal(err)
	}
	c := out.Checks[0]
	if c.State != StateStale || c.Confidence != ConfidenceUnknown || c.ResponseTime != nil || c.Evidence.ResponseTime == nil || c.ExpectationMatched == nil || !*c.ExpectationMatched {
		t.Fatalf("freshness: %+v", c)
	}
	s.Measurements = nil
	out, err = AssessHTTPS(s)
	if err != nil || out.Checks[0].State != StateUnknown || out.Checks[0].Evidence != nil {
		t.Fatal("invented missing evidence")
	}
	s.Selections = nil
	out, err = AssessHTTPS(s)
	if err != nil || len(out.Checks) != 0 {
		t.Fatal("invented default endpoint")
	}
}
func TestHTTPSRejectsInconsistentEvidence(t *testing.T) {
	for name, edit := range map[string]func(*HTTPSSnapshot){
		"invalid observer":      func(s *HTTPSSnapshot) { s.Observer.InterfaceIndex = 0 },
		"invalid time":          func(s *HTTPSSnapshot) { s.AsOf = time.Time{} },
		"overflow time":         func(s *HTTPSSnapshot) { s.AsOf = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) },
		"future window":         func(s *HTTPSSnapshot) { s.WindowStart = s.AsOf.Add(time.Second) },
		"wide window":           func(s *HTTPSSnapshot) { s.WindowStart = s.AsOf.Add(-MaxWindow - time.Second) },
		"freshness":             func(s *HTTPSSnapshot) { s.Freshness = 0 },
		"too many targets":      func(s *HTTPSSnapshot) { s.Selections = make([]HTTPSSelection, MaxTargets+1) },
		"too many samples":      func(s *HTTPSSnapshot) { s.Measurements = make([]HTTPSMeasurement, MaxMeasurements+1) },
		"duplicate selection":   func(s *HTTPSSnapshot) { s.Selections = append(s.Selections, s.Selections[0]) },
		"duplicate measurement": func(s *HTTPSSnapshot) { s.Measurements = append(s.Measurements, s.Measurements[0]) },
		"tied result": func(s *HTTPSSnapshot) {
			m := s.Measurements[0]
			m.ID = "tie"
			m.CompletedAt = m.CompletedAt.In(time.FixedZone("offset", 3600))
			s.Measurements = append(s.Measurements, m)
		},
		"changed expectation": func(s *HTTPSSnapshot) { s.Measurements[0].Selection.ExpectedStatus = 500 },
		"changed endpoint":    func(s *HTTPSSnapshot) { s.Measurements[0].Selection.EndpointID = "endpoint.other" },
		"different observer":  func(s *HTTPSSnapshot) { s.Measurements[0].Observer.SensorID = "sensor.other" },
		"invalid ID":          func(s *HTTPSSnapshot) { s.Measurements[0].ID = "https://private.test" },
		"future evidence":     func(s *HTTPSSnapshot) { s.Measurements[0].CompletedAt = s.AsOf.Add(time.Second) },
		"before window":       func(s *HTTPSSnapshot) { s.Measurements[0].StartedAt = s.WindowStart.Add(-time.Second) },
		"negative interval":   func(s *HTTPSSnapshot) { s.Measurements[0].StartedAt = s.AsOf },
		"duration": func(s *HTTPSSnapshot) {
			s.Measurements[0].StartedAt = s.Measurements[0].CompletedAt.Add(-MaxCheckTime - time.Nanosecond)
		},
		"unknown exchange":           func(s *HTTPSSnapshot) { s.Measurements[0].Exchange = "internet-down" },
		"unknown stage":              func(s *HTTPSSnapshot) { s.Measurements[0].Stage = "redirect-followed" },
		"unknown request":            func(s *HTTPSSnapshot) { s.Measurements[0].Request = "maybe" },
		"unverified TLS response":    func(s *HTTPSSnapshot) { s.Measurements[0].Stage = HTTPSTLS },
		"unsent response":            func(s *HTTPSSnapshot) { s.Measurements[0].Request = HTTPSRequestNotSent },
		"uncertain response":         func(s *HTTPSSnapshot) { s.Measurements[0].Request = HTTPSRequestUncertain },
		"informational is not final": func(s *HTTPSSnapshot) { s.Measurements[0].StatusCode = 103 },
		"invalid status":             func(s *HTTPSSnapshot) { s.Measurements[0].StatusCode = 600 },
		"gap with response":          func(s *HTTPSSnapshot) { s.Measurements[0].Gap = GapSleep },
		"TLS failure with status":    func(s *HTTPSSnapshot) { s.Measurements[0].Exchange = HTTPSTLSError },
		"negative latency":           func(s *HTTPSSnapshot) { *s.Measurements[0].ResponseTime = -1 },
		"latency outside interval":   func(s *HTTPSSnapshot) { *s.Measurements[0].ResponseTime = time.Hour },
	} {
		t.Run(name, func(t *testing.T) {
			s := httpsFixture()
			edit(&s)
			if _, err := AssessHTTPS(s); err == nil {
				t.Fatal("accepted invalid HTTPS evidence")
			}
		})
	}
	for _, edit := range []func(*HTTPSSelection){
		func(s *HTTPSSelection) { s.ID = "" }, func(s *HTTPSSelection) { s.EndpointID = strings.Repeat("x", 129) }, func(s *HTTPSSelection) { s.RequestID = "/private" },
		func(s *HTTPSSelection) { s.Method = "POST" }, func(s *HTTPSSelection) { s.Family = FamilyNone }, func(s *HTTPSSelection) { s.ExpectedStatus = 101 }, func(s *HTTPSSelection) { s.ExpectedStatus = 600 },
	} {
		s := httpsFixture().Selections[0]
		edit(&s)
		if ValidateHTTPSSelection(s) == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}

func TestHTTPSRejectsContradictoryFailureAndGapStages(t *testing.T) {
	for name, edit := range map[string]func(*HTTPSMeasurement){
		"connect error after TLS":       func(m *HTTPSMeasurement) { m.Stage = HTTPSTLS; m.Exchange = HTTPSConnectError },
		"TLS error after request":       func(m *HTTPSMeasurement) { m.Stage = HTTPSRequest; m.Exchange = HTTPSTLSError },
		"HTTP bytes before TLS":         func(m *HTTPSMeasurement) { m.Request = HTTPSRequestUncertain },
		"protocol error before request": func(m *HTTPSMeasurement) { m.Exchange = HTTPSProtocolError },
		"protocol error uncertain write": func(m *HTTPSMeasurement) {
			m.Stage = HTTPSRequest
			m.Request = HTTPSRequestUncertain
			m.Exchange = HTTPSProtocolError
		},
		"zero timeout":            func(m *HTTPSMeasurement) { m.Exchange = HTTPSTimeout; m.CompletedAt = m.StartedAt },
		"timing without response": func(m *HTTPSMeasurement) { d := time.Duration(0); m.ResponseTime = &d },
		"status without response": func(m *HTTPSMeasurement) { m.StatusCode = 200 },
		"gap without reason":      func(m *HTTPSMeasurement) { m.Exchange = HTTPSNotMeasured; m.Stage = "" },
		"gap with network stage":  func(m *HTTPSMeasurement) { m.Exchange = HTTPSNotMeasured; m.Gap = GapSleep },
		"unknown gap":             func(m *HTTPSMeasurement) { m.Exchange = HTTPSNotMeasured; m.Stage = ""; m.Gap = "maybe-sleep" },
	} {
		t.Run(name, func(t *testing.T) {
			s := httpsFixture()
			m := &s.Measurements[0]
			m.Exchange = HTTPSIncomplete
			m.Stage = HTTPSConnect
			m.Request = HTTPSRequestNotSent
			m.StatusCode = 0
			m.ResponseTime = nil
			edit(m)
			if _, err := AssessHTTPS(s); err == nil {
				t.Fatal("accepted contradiction")
			}
		})
	}
}
