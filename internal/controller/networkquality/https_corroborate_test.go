package networkquality

import (
	"testing"
	"time"
)

func addHTTPS(in *CorroborationInput, status int) {
	s := httpsFixture()
	s.Observer.ScopeID = in.Network.Observer.ScopeID
	s.Observer.InterfaceName = in.Network.Observer.InterfaceName
	s.Observer.InterfaceIndex = in.Network.Observer.InterfaceIndex
	s.AsOf, s.WindowStart, s.Freshness = in.Network.AsOf, in.Network.WindowStart, in.Network.Freshness
	s.Measurements[0].Observer = s.Observer
	s.Measurements[0].StatusCode = status
	in.HTTPS = &s
	in.HTTPSDeviceID = in.NetworkDeviceID
}
func TestHTTPSCorroborationCountsErrorResponsesAsResponses(t *testing.T) {
	for _, status := range []int{204, 302, 404, 503, 511} {
		in := comparisonFixture()
		addHTTPS(&in, status)
		failICMP(&in)
		// Isolate ICMP and HTTPS: a DNS response must not mask a mistaken HTTP model.
		in.Resolvers.Selections = nil
		in.Resolvers.Measurements = nil
		out, err := Corroborate(in)
		if err != nil || out.Conclusion != "icmp-misses-with-responses" || len(out.Compared) != 2 {
			t.Fatalf("%d: %+v %v", status, out, err)
		}
		for _, e := range out.Evidence {
			if e.Method == MethodHTTPS {
				if e.HTTPStatus != status || e.HTTPSStage != HTTPSRequest || e.HTTPSRequest != HTTPSRequestAccepted || e.HTTPSSelection == nil || e.ExpectationMatched == nil || *e.ExpectationMatched != (status == 204) || e.Attempts != 0 || e.Successes != 0 {
					t.Fatalf("lost protocol evidence: %+v", e)
				}
				in.HTTPS.Selections[0].ExpectedStatus = 500
				if e.HTTPSSelection.ExpectedStatus != 204 {
					t.Fatal("aliased selection")
				}
			}
		}
	}
}
func TestHTTPSCorroborationDoesNotInventRepliesOrOtherLayers(t *testing.T) {
	for _, exchange := range []HTTPSExchange{HTTPSConnectError, HTTPSTLSError, HTTPSTimeout, HTTPSIncomplete} {
		in := comparisonFixture()
		addHTTPS(&in, 204)
		failICMP(&in)
		in.Resolvers.Selections = nil
		in.Resolvers.Measurements = nil
		m := &in.HTTPS.Measurements[0]
		m.StatusCode = 0
		m.ResponseTime = nil
		m.Request = HTTPSRequestNotSent
		m.Stage = HTTPSConnect
		m.Exchange = exchange
		if exchange == HTTPSTLSError {
			m.Stage = HTTPSTLS
		}
		out, err := Corroborate(in)
		want := "problems-across-selected-layers"
		if exchange == HTTPSIncomplete {
			want = "insufficient-evidence"
		}
		if err != nil || out.Conclusion != want {
			t.Fatalf("%s: %+v %v", exchange, out, err)
		}
		in.Network.Targets = nil
		in.Network.Measurements = nil
		out, err = Corroborate(in)
		if err != nil || out.Conclusion != "insufficient-evidence" {
			t.Fatal("single external check became wider conclusion")
		}
	}
}
func TestHTTPSCorroborationRejectsContextAndGenericCounts(t *testing.T) {
	for name, edit := range map[string]func(*CorroborationInput){
		"device":       func(in *CorroborationInput) { in.HTTPSDeviceID = "other" },
		"scope":        func(in *CorroborationInput) { in.HTTPS.Observer.ScopeID = "other" },
		"interface":    func(in *CorroborationInput) { in.HTTPS.Observer.InterfaceIndex++ },
		"time":         func(in *CorroborationInput) { in.HTTPS.AsOf = in.HTTPS.AsOf.Add(time.Second) },
		"freshness":    func(in *CorroborationInput) { in.HTTPS.Freshness += time.Second },
		"duplicate ID": func(in *CorroborationInput) { in.HTTPS.Measurements[0].ID = in.Resolvers.Measurements[0].ID },
		"unsupported counts": func(in *CorroborationInput) {
			in.Network.Targets[0].Layer = LayerExternal
			in.Network.Targets[0].Method = MethodHTTPS
		},
		"missing snapshot": func(in *CorroborationInput) { in.HTTPS = nil },
		"combined bound": func(in *CorroborationInput) {
			for i := 0; i < MaxTargets; i++ {
				s := in.HTTPS.Selections[0]
				s.ID = string(rune('a' + i))
				in.HTTPS.Selections = append(in.HTTPS.Selections, s)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := comparisonFixture()
			addHTTPS(&in, 204)
			edit(&in)
			if _, err := Corroborate(in); err == nil {
				t.Fatal("accepted invalid comparison")
			}
		})
	}
}
func TestHTTPSCorroborationKeepsFreshnessFamilyAndDiscontinuities(t *testing.T) {
	in := comparisonFixture()
	addHTTPS(&in, 204)
	in.HTTPS.Selections[0].Family = FamilyIPv6
	in.HTTPS.Measurements[0].Selection = in.HTTPS.Selections[0]
	out, err := Corroborate(in)
	if err != nil || len(out.Compared) != 2 {
		t.Fatal("crossed address families")
	}
	// A real runtime gap in the other family still separates this observing point.
	m := in.HTTPS.Measurements[0]
	m.ID = "https.gap"
	m.Exchange = HTTPSNotMeasured
	m.Stage = ""
	m.Request = HTTPSRequestNotSent
	m.StatusCode = 0
	m.ResponseTime = nil
	m.Gap = GapNetworkChanged
	m.StartedAt = in.Network.AsOf.Add(-4 * time.Second)
	m.CompletedAt = m.StartedAt
	in.HTTPS.Measurements = append(in.HTTPS.Measurements, m)
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "collection-discontinuity" || out.DiscontinuityID != "https.gap" {
		t.Fatalf("erased gap: %+v %v", out, err)
	}
	in = comparisonFixture()
	addHTTPS(&in, 204)
	in.Resolvers.Selections = nil
	in.Resolvers.Measurements = nil
	in.HTTPS.Measurements[0].StartedAt = in.Network.AsOf.Add(-62 * time.Second)
	in.HTTPS.Measurements[0].CompletedAt = in.Network.AsOf.Add(-60 * time.Second)
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "insufficient-evidence" {
		t.Fatal("compared stale HTTPS")
	}
	for _, e := range out.Evidence {
		if e.Method == MethodHTTPS && (e.State != StateStale || e.ExpectationMatched == nil || !*e.ExpectationMatched) {
			t.Fatal("lost historical expectation")
		}
	}
}

func TestHTTPSComparisonRejectsAmbiguousExternalReferences(t *testing.T) {
	in := comparisonFixture()
	addHTTPS(&in, 204)
	in.Network.Targets[0].Layer = LayerExternal
	in.HTTPS.Selections[0].ID = in.Network.Targets[0].ID
	in.HTTPS.Measurements[0].Selection = in.HTTPS.Selections[0]
	if _, err := Corroborate(in); err == nil {
		t.Fatal("accepted ambiguous references within external layer")
	}
}
func TestHTTPSComparisonDoesNotUseSameLayerRepliesToCorroborateFailure(t *testing.T) {
	in := comparisonFixture()
	addHTTPS(&in, 503)
	in.Network.Targets = nil
	in.Network.Measurements = nil
	in.Resolvers.Selections = nil
	in.Resolvers.Measurements = nil
	m := in.HTTPS.Measurements[0]
	m.ID = "measurement.other"
	m.Selection.ID = "selection.other"
	m.Selection.EndpointID = "endpoint.other"
	m.StatusCode = 204
	in.HTTPS.Selections = append(in.HTTPS.Selections, m.Selection)
	in.HTTPS.Measurements = append(in.HTTPS.Measurements, m)
	out, err := Corroborate(in)
	if err != nil || out.Conclusion != "insufficient-evidence" || len(out.Compared) != 0 {
		t.Fatalf("external layer corroborated itself: %+v %v", out, err)
	}
	addLink(&in, true)
	out, err = Corroborate(in)
	if err != nil || out.Conclusion != "mixed-or-limited-evidence" {
		t.Fatalf("external success supported own failure: %+v %v", out, err)
	}
}
func TestHTTPSComparisonNeverSelectsOlderAlignedSuccess(t *testing.T) {
	in := comparisonFixture()
	addHTTPS(&in, 204)
	// Keep the ICMP sample fresh but outside the explicit five-second completion skew.
	in.Network.Measurements[0].StartedAt = in.Network.AsOf.Add(-13 * time.Second)
	in.Network.Measurements[0].CompletedAt = in.Network.AsOf.Add(-10 * time.Second)
	old := in.HTTPS.Measurements[0]
	old.ID = "https.old"
	old.StartedAt = in.Network.AsOf.Add(-12 * time.Second)
	old.CompletedAt = in.Network.AsOf.Add(-10 * time.Second)
	in.HTTPS.Measurements = append(in.HTTPS.Measurements, old)
	out, err := Corroborate(in)
	if err != nil || out.Conclusion != "observations-too-far-apart" || len(out.Compared) != 0 {
		t.Fatalf("substituted older HTTPS evidence: %+v %v", out, err)
	}
}
