package networkquality

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Entirely synthetic: immutable configuration references only, no destinations,
// host DNS APIs, traffic, packet parsing, or controller execution authority.
func resolverFixture() ResolverSnapshot {
	asOf := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	observer := Observer{ScopeID: "scope-home", SensorID: "sensor-desktop", InterfaceName: "en0", InterfaceIndex: 7}
	selection := ResolverSelection{ID: "resolver-query-v4", ResolverID: "resolver-config-1", QueryID: "query-config-1", Family: FamilyIPv4, Transport: DNSUDP, QueryType: DNSQueryA, Expect: DNSExpectAnswer}
	zero := time.Duration(0)
	return ResolverSnapshot{Observer: observer, AsOf: asOf, WindowStart: asOf.Add(-time.Hour), Freshness: time.Minute, Selections: []ResolverSelection{selection},
		Measurements: []ResolverMeasurement{{ID: "dns-1", Selection: selection, Observer: observer, StartedAt: asOf.Add(-3 * time.Second), CompletedAt: asOf.Add(-time.Second),
			Exchange: DNSResponseReceived, Request: DNSRequestAccepted, Reply: &DNSReply{Answer: DNSAnswerPresent}, ResponseTime: &zero}}}
}

func TestResolverResponseAndTransportMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reply    *DNSReply
		exchange DNSExchangeState
		request  DNSRequestState
		result   ResolverResult
		state    State
		summary  string
	}{
		{"answer", &DNSReply{Answer: DNSAnswerPresent}, DNSResponseReceived, DNSRequestAccepted, ResolverAnswer, StateSucceeded, "returned an answer"},
		{"nxdomain is a response", &DNSReply{RCode: 3}, DNSResponseReceived, DNSRequestAccepted, ResolverNXDOMAIN, StateIssueObserved, "NXDOMAIN"},
		{"nodata is not nxdomain", &DNSReply{Answer: DNSAnswerNoData}, DNSResponseReceived, DNSRequestAccepted, ResolverNoData, StateIssueObserved, "NODATA"},
		{"refused is not transport failure", &DNSReply{RCode: 5}, DNSResponseReceived, DNSRequestAccepted, ResolverRefused, StateIssueObserved, "REFUSED"},
		{"server failure", &DNSReply{RCode: 2}, DNSResponseReceived, DNSRequestAccepted, ResolverServerFailure, StateIssueObserved, "SERVFAIL"},
		{"format error", &DNSReply{RCode: 1}, DNSResponseReceived, DNSRequestAccepted, ResolverFormatError, StateIssueObserved, "FORMERR"},
		{"not implemented", &DNSReply{RCode: 4}, DNSResponseReceived, DNSRequestAccepted, ResolverNotImplemented, StateIssueObserved, "NOTIMP"},
		{"extended error", &DNSReply{RCode: 16}, DNSResponseReceived, DNSRequestAccepted, ResolverOtherError, StateIssueObserved, "another DNS error"},
		{"unknown error preserved", &DNSReply{RCode: 4095}, DNSResponseReceived, DNSRequestAccepted, ResolverOtherError, StateIssueObserved, "another DNS error"},
		{"truncation not successful lookup", &DNSReply{Truncated: true}, DNSResponseReceived, DNSRequestAccepted, ResolverTruncated, StateUnknown, "truncated"},
		{"truncated error not conclusive", &DNSReply{RCode: 3, Truncated: true}, DNSResponseReceived, DNSRequestAccepted, ResolverTruncated, StateUnknown, "truncated"},
		{"referral not followed", &DNSReply{Answer: DNSAnswerReferral}, DNSResponseReceived, DNSRequestAccepted, ResolverReferral, StateUnknown, "referral"},
		{"unclassified is not nodata", &DNSReply{Answer: DNSAnswerUnclassified}, DNSResponseReceived, DNSRequestAccepted, ResolverUnclassified, StateUnknown, "could not be classified"},
		{"timeout", nil, DNSTimeout, DNSRequestAccepted, ResolverTimeout, StateIssueObserved, "No matched DNS reply"},
		{"socket failure before send", nil, DNSTransportError, DNSRequestNotSent, ResolverTransportFailure, StateIssueObserved, "transport error"},
		{"socket failure after accepted", nil, DNSTransportError, DNSRequestAccepted, ResolverTransportFailure, StateIssueObserved, "transport error"},
		{"uncertain send failure", nil, DNSTransportError, DNSRequestUncertain, ResolverTransportFailure, StateIssueObserved, "transport error"},
		{"interrupted accepted request", nil, DNSIncomplete, DNSRequestAccepted, ResolverIncomplete, StateUnknown, "incomplete"},
		{"interrupted uncertain request", nil, DNSIncomplete, DNSRequestUncertain, ResolverIncomplete, StateUnknown, "incomplete"},
		{"interrupted before send", nil, DNSIncomplete, DNSRequestNotSent, ResolverIncomplete, StateUnknown, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := resolverFixture()
			m := &s.Measurements[0]
			m.Reply = tc.reply
			m.Exchange = tc.exchange
			m.Request = tc.request
			if m.Reply == nil {
				m.ResponseTime = nil
			}
			report, err := AssessResolvers(s)
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			if check.State != tc.state || check.Result != tc.result || !strings.Contains(check.Summary, tc.summary) || check.NextStep == "" {
				t.Fatalf("unexpected diagnosis: %+v", check)
			}
			confidence := ConfidenceLimited
			if tc.exchange == DNSIncomplete {
				confidence = ConfidenceUnknown
			}
			if check.Confidence != confidence || check.Selection != s.Selections[0] || !reflect.DeepEqual(check.Evidence, m) || report.Observer != s.Observer {
				t.Fatalf("lost context: %+v", report)
			}
			if (check.ResponseTime != nil) != (tc.reply != nil) {
				t.Fatal("response timing became successful-lookup timing or was invented")
			}
			if check.ResponseTime != nil && *check.ResponseTime != 0 {
				t.Fatal("measured zero not preserved")
			}
		})
	}
}

func TestResolverQueryExpectationsAreExplicitAndIndependent(t *testing.T) {
	for _, expect := range []DNSExpectation{DNSExpectAnswer, DNSExpectNXDOMAIN, DNSExpectNoData} {
		for _, reply := range []DNSReply{{Answer: DNSAnswerPresent}, {RCode: 3}, {Answer: DNSAnswerNoData}} {
			s := resolverFixture()
			s.Selections[0].Expect = expect
			s.Measurements[0].Selection = s.Selections[0]
			s.Measurements[0].Reply = &reply
			report, err := AssessResolvers(s)
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			matched := (expect == DNSExpectAnswer && reply.Answer == DNSAnswerPresent) || (expect == DNSExpectNXDOMAIN && reply.RCode == 3) || (expect == DNSExpectNoData && reply.Answer == DNSAnswerNoData)
			if (check.State == StateSucceeded) != matched || !strings.Contains(check.Summary, map[bool]string{true: "matches", false: "differs"}[matched]) {
				t.Fatalf("expectation changed response semantics: %+v", check)
			}
			if check.Evidence.Reply.RCode != reply.RCode || check.ResponseTime == nil {
				t.Fatal("negative response evidence was erased")
			}
		}
	}
	// AAAA over IPv4 and A over IPv6 are valid, independent transport/question axes.
	for _, family := range []AddressFamily{FamilyIPv4, FamilyIPv6} {
		for _, query := range []DNSQueryType{DNSQueryA, DNSQueryAAAA} {
			for _, transport := range []DNSTransport{DNSUDP, DNSTCP} {
				s := resolverFixture()
				s.Selections[0].Family = family
				s.Selections[0].QueryType = query
				s.Selections[0].Transport = transport
				s.Measurements[0].Selection = s.Selections[0]
				if _, err := AssessResolvers(s); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestResolverGapsAndMissingEvidenceNeverBecomeOutages(t *testing.T) {
	for _, gap := range []GapReason{GapPermission, GapUnsupported, GapSourceUnavailable, GapDisabled, GapNotConfigured, GapSleep, GapOffline, GapNetworkChanged} {
		t.Run(string(gap), func(t *testing.T) {
			s := resolverFixture()
			previous := s.Measurements[0]
			m := previous
			m.ID = "gap-1"
			m.Exchange = DNSNotMeasured
			m.Request = DNSRequestNotSent
			m.Gap = gap
			m.Reply = nil
			m.ResponseTime = nil
			m.StartedAt = s.WindowStart
			m.CompletedAt = s.AsOf
			s.Measurements = append(s.Measurements, m)
			report, err := AssessResolvers(s)
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			if check.State != StateNotMeasured || check.Result != ResolverNotMeasured || check.Evidence.ID != m.ID || check.Evidence.Gap != gap || check.ResponseTime != nil || check.Confidence != ConfidenceUnknown {
				t.Fatalf("gap was hidden by prior success: %+v", check)
			}
		})
	}
	s := resolverFixture()
	s.Measurements = nil
	report, err := AssessResolvers(s)
	if err != nil || report.Checks[0].State != StateUnknown || report.Checks[0].Evidence != nil || !report.Checks[0].FreshUntil.IsZero() || report.Checks[0].ResponseTime != nil {
		t.Fatal("missing evidence invented measurement", err)
	}
	s.Selections = nil
	report, err = AssessResolvers(s)
	if err != nil || len(report.Checks) != 0 || report.Checks == nil {
		t.Fatal("empty configuration invented checks", err)
	}
}

func TestResolverFreshnessOrderRecoveryAndPointerIsolation(t *testing.T) {
	for _, age := range []time.Duration{time.Minute - time.Nanosecond, time.Minute, time.Minute + time.Nanosecond} {
		s := resolverFixture()
		m := &s.Measurements[0]
		m.CompletedAt = s.AsOf.Add(-age)
		m.StartedAt = m.CompletedAt.Add(-time.Second)
		report, err := AssessResolvers(s)
		if err != nil {
			t.Fatal(err)
		}
		check := report.Checks[0]
		if (check.State == StateStale) != (age >= s.Freshness) || !check.FreshUntil.Equal(m.CompletedAt.Add(s.Freshness)) {
			t.Fatalf("bad freshness boundary: %+v", check)
		}
		if age >= s.Freshness && (check.ResponseTime != nil || check.Confidence != ConfidenceUnknown) {
			t.Fatal("replay restored current timing")
		}
		if check.Evidence.ResponseTime == nil || check.Evidence.Reply == nil || check.Result != ResolverAnswer {
			t.Fatal("historical evidence lost")
		}
	}
	s := resolverFixture()
	old := s.Measurements[0]
	old.ID = "old-replayed"
	old.StartedAt = old.StartedAt.Add(-time.Minute)
	old.CompletedAt = old.CompletedAt.Add(-time.Minute)
	s.Measurements[0].Reply = &DNSReply{RCode: 2}
	s.Measurements = append(s.Measurements, old)
	first, err := AssessResolvers(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Measurements[0], s.Measurements[1] = s.Measurements[1], s.Measurements[0]
	second, err := AssessResolvers(s)
	if err != nil || !reflect.DeepEqual(first, second) || first.Checks[0].Result != ResolverServerFailure {
		t.Fatal("replay/input order changed result", err)
	}
	newer := old
	newer.ID = "recovered"
	newer.StartedAt = s.AsOf.Add(-time.Second)
	newer.CompletedAt = s.AsOf
	s.Measurements = append(s.Measurements, newer)
	recovered, err := AssessResolvers(s)
	if err != nil || recovered.Checks[0].State != StateSucceeded {
		t.Fatal("new evidence did not recover the selected check", err)
	}
	check := recovered.Checks[0]
	*check.ResponseTime = time.Second
	*check.Evidence.ResponseTime = 2 * time.Second
	check.Evidence.Reply.RCode = 3
	if *newer.ResponseTime != 0 || newer.Reply.RCode != 0 || *s.Measurements[0].ResponseTime != 0 {
		t.Fatal("assessment aliases input")
	}
	if *check.ResponseTime == *check.Evidence.ResponseTime {
		t.Fatal("current and retained timings alias")
	}
}

func TestResolverTargetsRemainIndependent(t *testing.T) {
	s := resolverFixture()
	for _, change := range []func(*ResolverSelection){
		func(v *ResolverSelection) { v.ResolverID = "resolver-config-2" }, func(v *ResolverSelection) { v.QueryID = "query-config-2" },
		func(v *ResolverSelection) { v.Family = FamilyIPv6 }, func(v *ResolverSelection) { v.QueryType = DNSQueryAAAA }, func(v *ResolverSelection) { v.Transport = DNSTCP },
	} {
		selection := s.Selections[0]
		selection.ID = fmt.Sprintf("selection-%d", len(s.Selections))
		change(&selection)
		s.Selections = append(s.Selections, selection)
	}
	report, err := AssessResolvers(s)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range report.Checks {
		if i == 0 {
			if c.State != StateSucceeded {
				t.Fatal(c)
			}
		} else if c.State != StateUnknown || c.Evidence != nil {
			t.Fatal("one result masked another resolver/query/family/transport", c)
		}
	}
}

func TestResolverValidationRejectsContradictionsWithoutEchoingInputs(t *testing.T) {
	edits := map[string]func(*ResolverSnapshot){
		"observer":             func(s *ResolverSnapshot) { s.Observer.ScopeID = "private secret" },
		"large index":          func(s *ResolverSnapshot) { s.Observer.InterfaceIndex = 2147483648 },
		"zero time":            func(s *ResolverSnapshot) { s.AsOf = time.Time{} },
		"unrepresentable time": func(s *ResolverSnapshot) { s.AsOf = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) },
		"future window":        func(s *ResolverSnapshot) { s.WindowStart = s.AsOf.Add(time.Second) },
		"wide window":          func(s *ResolverSnapshot) { s.WindowStart = s.AsOf.Add(-MaxWindow - time.Nanosecond) },
		"zero freshness":       func(s *ResolverSnapshot) { s.Freshness = 0 },
		"wide freshness":       func(s *ResolverSnapshot) { s.Freshness = MaxFreshness + time.Nanosecond },
		"too many selections":  func(s *ResolverSnapshot) { s.Selections = make([]ResolverSelection, MaxTargets+1) },
		"too much evidence":    func(s *ResolverSnapshot) { s.Measurements = make([]ResolverMeasurement, MaxMeasurements+1) },
		"duplicate selection":  func(s *ResolverSnapshot) { s.Selections = append(s.Selections, s.Selections[0]) },
		"empty expectation":    func(s *ResolverSnapshot) { s.Selections[0].Expect = "" },
		"invalid query type":   func(s *ResolverSnapshot) { s.Selections[0].QueryType = "ANY" },
		"unknown transport":    func(s *ResolverSnapshot) { s.Selections[0].Transport = "https" },
		"missing resolver":     func(s *ResolverSnapshot) { s.Selections[0].ResolverID = "" },
		"oversized reference":  func(s *ResolverSnapshot) { s.Selections[0].QueryID = strings.Repeat("a", 129) },
		"family":               func(s *ResolverSnapshot) { s.Selections[0].Family = FamilyNone },
		"unknown selection":    func(s *ResolverSnapshot) { s.Measurements[0].Selection.ID = "unknown" },
		"changed resolver":     func(s *ResolverSnapshot) { s.Measurements[0].Selection.ResolverID = "resolver-config-2" },
		"changed query":        func(s *ResolverSnapshot) { s.Measurements[0].Selection.QueryID = "query-config-2" },
		"changed expectation":  func(s *ResolverSnapshot) { s.Measurements[0].Selection.Expect = DNSExpectNXDOMAIN },
		"changed family":       func(s *ResolverSnapshot) { s.Measurements[0].Selection.Family = FamilyIPv6 },
		"changed observer":     func(s *ResolverSnapshot) { s.Measurements[0].Observer.InterfaceIndex++ },
		"duplicate evidence":   func(s *ResolverSnapshot) { s.Measurements = append(s.Measurements, s.Measurements[0]) },
		"tied evidence": func(s *ResolverSnapshot) {
			m := s.Measurements[0]
			m.ID = "tie"
			m.CompletedAt = m.CompletedAt.In(time.FixedZone("offset", 3600))
			s.Measurements = append(s.Measurements, m)
		},
		"future result": func(s *ResolverSnapshot) { s.Measurements[0].CompletedAt = s.AsOf.Add(time.Nanosecond) },
		"inverted time": func(s *ResolverSnapshot) {
			s.Measurements[0].StartedAt = s.Measurements[0].CompletedAt.Add(time.Nanosecond)
		},
		"out of window": func(s *ResolverSnapshot) { s.Measurements[0].StartedAt = s.WindowStart.Add(-time.Nanosecond) },
		"duration": func(s *ResolverSnapshot) {
			s.Measurements[0].StartedAt = s.Measurements[0].CompletedAt.Add(-MaxCheckTime - time.Nanosecond)
		},
		"missing request state":   func(s *ResolverSnapshot) { s.Measurements[0].Request = "" },
		"unknown exchange":        func(s *ResolverSnapshot) { s.Measurements[0].Exchange = "internet-down" },
		"unsent reply":            func(s *ResolverSnapshot) { s.Measurements[0].Request = DNSRequestNotSent },
		"uncertain reply":         func(s *ResolverSnapshot) { s.Measurements[0].Request = DNSRequestUncertain },
		"missing reply":           func(s *ResolverSnapshot) { s.Measurements[0].Reply = nil },
		"reply on timeout":        func(s *ResolverSnapshot) { s.Measurements[0].Exchange = DNSTimeout },
		"negative code":           func(s *ResolverSnapshot) { s.Measurements[0].Reply = &DNSReply{RCode: -1} },
		"large code":              func(s *ResolverSnapshot) { s.Measurements[0].Reply = &DNSReply{RCode: 4096} },
		"answer on error":         func(s *ResolverSnapshot) { s.Measurements[0].Reply.RCode = 3 },
		"answer on truncation":    func(s *ResolverSnapshot) { s.Measurements[0].Reply.Truncated = true },
		"no classification":       func(s *ResolverSnapshot) { s.Measurements[0].Reply.Answer = "" },
		"invented classification": func(s *ResolverSnapshot) { s.Measurements[0].Reply.Answer = "secure" },
		"negative latency":        func(s *ResolverSnapshot) { *s.Measurements[0].ResponseTime = -1 },
		"overlong latency":        func(s *ResolverSnapshot) { *s.Measurements[0].ResponseTime = MaxCheckTime },
		"gap with reply":          func(s *ResolverSnapshot) { s.Measurements[0].Gap = GapSleep },
		"unmeasured with metrics": func(s *ResolverSnapshot) {
			s.Measurements[0].Exchange = DNSNotMeasured
			s.Measurements[0].Gap = GapPermission
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			s := resolverFixture()
			edit(&s)
			report, err := AssessResolvers(s)
			if err == nil || strings.Contains(err.Error(), "private") || !reflect.DeepEqual(report, ResolverReport{}) {
				t.Fatalf("invalid input escaped: %+v %v", report, err)
			}
		})
	}
	for _, request := range []DNSRequestState{DNSRequestNotSent, DNSRequestUncertain} {
		s := resolverFixture()
		m := &s.Measurements[0]
		m.Exchange = DNSTimeout
		m.Request = request
		m.Reply = nil
		m.ResponseTime = nil
		if _, err := AssessResolvers(s); err == nil {
			t.Fatal("unsent/uncertain request became a timeout")
		}
	}
	for _, exchange := range []DNSExchangeState{DNSTransportError, DNSIncomplete, DNSNotMeasured} {
		s := resolverFixture()
		m := &s.Measurements[0]
		m.Exchange = exchange
		m.Request = DNSRequestNotSent
		m.Reply = nil
		if _, err := AssessResolvers(s); err == nil {
			t.Fatal("non-response carried response latency")
		}
	}
}

func TestResolverInputBoundsAreInclusive(t *testing.T) {
	s := resolverFixture()
	s.WindowStart = s.AsOf.Add(-MaxWindow)
	s.Freshness = MaxFreshness
	s.Measurements = nil
	for i := 1; i < MaxTargets; i++ {
		selection := s.Selections[0]
		selection.ID = fmt.Sprintf("selection-%d", i)
		s.Selections = append(s.Selections, selection)
	}
	for i := 0; i < MaxMeasurements; i++ {
		m := resolverFixture().Measurements[0]
		m.ID = fmt.Sprintf("dns-%d", i)
		m.Selection = s.Selections[i%MaxTargets]
		m.CompletedAt = s.AsOf.Add(-time.Duration(i) * time.Second)
		m.StartedAt = m.CompletedAt.Add(-MaxCheckTime)
		s.Measurements = append(s.Measurements, m)
	}
	if report, err := AssessResolvers(s); err != nil || len(report.Checks) != MaxTargets {
		t.Fatal("inclusive bounds rejected", err)
	}
}

func TestResolverUnknownMetricsAndInvalidGaps(t *testing.T) {
	s := resolverFixture()
	s.Measurements[0].ResponseTime = nil
	report, err := AssessResolvers(s)
	if err != nil || report.Checks[0].State != StateSucceeded || report.Checks[0].ResponseTime != nil || report.Checks[0].Evidence.ResponseTime != nil {
		t.Fatal("missing response time became zero or failure", err)
	}
	for _, mode := range []string{"zero-timeout", "unknown-gap", "sent-gap", "uncertain-gap", "freshness-overflow"} {
		t.Run(mode, func(t *testing.T) {
			s := resolverFixture()
			m := &s.Measurements[0]
			m.Reply = nil
			m.ResponseTime = nil
			switch mode {
			case "zero-timeout":
				m.Exchange = DNSTimeout
				m.StartedAt = m.CompletedAt
			case "unknown-gap":
				m.Exchange = DNSNotMeasured
				m.Request = DNSRequestNotSent
				m.Gap = "isp-outage"
			case "sent-gap":
				m.Exchange = DNSNotMeasured
				m.Gap = GapSleep
			case "uncertain-gap":
				m.Exchange = DNSNotMeasured
				m.Request = DNSRequestUncertain
				m.Gap = GapNetworkChanged
			case "freshness-overflow":
				s.AsOf = time.Unix(0, 1<<63-1)
				s.WindowStart = s.AsOf.Add(-time.Hour)
				s.Measurements = nil
			}
			if _, err := AssessResolvers(s); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
