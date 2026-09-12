package localapi

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func httpsContractReview(t *testing.T) api.HTTPSCheckReview {
	t.Helper()
	at := time.Now().Round(0).UTC()
	p, err := httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https-selection." + strings.Repeat("a", 32), EndpointID: "https-endpoint." + strings.Repeat("b", 32), RequestID: "https-request." + strings.Repeat("c", 32), Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: "private.example", RequestTarget: "/check?value=1", DestinationPolicy: httpsplan.ExactEndpoint}, at)
	if err != nil {
		t.Fatal(err)
	}
	observed := at.Add(-time.Second)
	r := projectHTTPSReview(httpsrun.Review{Selection: httpsrun.Selection{Plan: p, RouteObservedAt: observed, RouteFreshUntil: observed.Add(httpsplan.ReviewLifetime)}, ExpiresAt: observed.Add(httpsplan.ReviewLifetime)}, strings.Repeat("d", 32))
	if err := validateHTTPSReview(r, r.SelectionID, at); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHTTPSConsentReviewRejectsAlteredDisclosure(t *testing.T) {
	r := httpsContractReview(t)
	changes := map[string]func(*api.HTTPSCheckReview){
		"mode":           func(r *api.HTTPSCheckReview) { r.Mode = "preview-only" },
		"challenge":      func(r *api.HTTPSCheckReview) { r.Challenge = "invalid" },
		"profile":        func(r *api.HTTPSCheckReview) { r.Profile = "other" },
		"endpoint":       func(r *api.HTTPSCheckReview) { r.Settings.Endpoint = "127.0.0.1:443" },
		"name-case":      func(r *api.HTTPSCheckReview) { r.Settings.ServerName = "Private.Example" },
		"target":         func(r *api.HTTPSCheckReview) { r.Settings.RequestTarget = "/changed" },
		"method":         func(r *api.HTTPSCheckReview) { r.Settings.Method = "GET" },
		"family":         func(r *api.HTTPSCheckReview) { r.Settings.Family = "ipv6" },
		"status":         func(r *api.HTTPSCheckReview) { r.Settings.ExpectedStatus = 600 },
		"source":         func(r *api.HTTPSCheckReview) { r.Source = "192.168.50.2" },
		"interface":      func(r *api.HTTPSCheckReview) { r.Binding.InterfaceIndex = 0 },
		"request":        func(r *api.HTTPSCheckReview) { r.RequestBytes += "Cookie: secret\r\n" },
		"privacy":        func(r *api.HTTPSCheckReview) { r.Privacy = nil },
		"privacy-text":   func(r *api.HTTPSCheckReview) { r.Privacy[0] = "No privacy impact" },
		"tls":            func(r *api.HTTPSCheckReview) { r.Policy.VerifyServerIdentity = false },
		"tls-min":        func(r *api.HTTPSCheckReview) { r.Policy.MinTLSVersion = "TLS 1.0" },
		"proxy":          func(r *api.HTTPSCheckReview) { r.Policy.UseProxy = true },
		"redirect":       func(r *api.HTTPSCheckReview) { r.Policy.FollowRedirects = true },
		"body":           func(r *api.HTTPSCheckReview) { r.Policy.ReadResponseBody = true },
		"request-budget": func(r *api.HTTPSCheckReview) { r.Budget.MaxRequestBytes++ },
		"read-budget":    func(r *api.HTTPSCheckReview) { r.Budget.MaxTransportReadBytes++ },
		"connect-budget": func(r *api.HTTPSCheckReview) { r.Budget.ConnectTimeoutMS++ },
		"tls-budget":     func(r *api.HTTPSCheckReview) { r.Budget.TLSHandshakeTimeoutMS++ },
		"total-budget":   func(r *api.HTTPSCheckReview) { r.Budget.TotalTimeoutMS++ },
		"cooldown":       func(r *api.HTTPSCheckReview) { r.Budget.MinRunIntervalMS = 0 },
		"retry":          func(r *api.HTTPSCheckReview) { r.Budget.MaxRetries = 1 },
		"external":       func(r *api.HTTPSCheckReview) { r.OutsideEnrolledPrefixes = false },
		"expired":        func(r *api.HTTPSCheckReview) { r.ExpiresAt = r.CreatedAt },
		"renewed-route":  func(r *api.HTTPSCheckReview) { r.RouteFreshUntil = r.RouteFreshUntil.Add(time.Second) },
		"renewed-review": func(r *api.HTTPSCheckReview) { r.ExpiresAt = r.ExpiresAt.Add(time.Second) },
		"future-route":   func(r *api.HTTPSCheckReview) { r.RouteObservedAt = r.CreatedAt.Add(time.Second) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := cloneHTTPSReview(r)
			change(&changed)
			if validateHTTPSReview(changed, r.SelectionID, r.CreatedAt) == nil {
				t.Fatal("altered disclosure accepted")
			}
		})
	}
	// Wire JSON roundtrip preserves exact request bytes and all disclosure fields.
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded api.HTTPSCheckReview
	if decodeStrictJSON(raw, &decoded) != nil || validateHTTPSReview(decoded, r.SelectionID, r.CreatedAt) != nil {
		t.Fatal("roundtrip lost disclosure")
	}
	if validateHTTPSReview(r, r.SelectionID, r.ExpiresAt) == nil {
		t.Fatal("expired review accepted")
	}
}

func TestHTTPSConsentResultEvidenceAndOwnership(t *testing.T) {
	r := httpsContractReview(t)
	zero := time.Duration(0)
	sample := &nq.HTTPSMeasurement{ID: strings.Repeat("e", 32), Selection: httpsWireSelection(r), Observer: httpsWireObserver(r), StartedAt: r.CreatedAt, CompletedAt: r.CreatedAt.Add(time.Millisecond), Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, StatusCode: 503, ResponseTime: &zero}
	out := projectHTTPSResult(r, httpsrun.Result{RunID: sample.ID, Outcome: "completed", Sample: sample}, nil)
	now := r.CreatedAt.Add(time.Second)
	if validateHTTPSResult(out, r, now) != nil {
		t.Fatal("503 conflated with execution failure")
	}
	sample.StatusCode = 204
	if out.Measurement.StatusCode != 503 {
		t.Fatal("sample alias")
	}
	clone := cloneHTTPSReview(r)
	clone.Privacy[0] = "changed"
	clone.Binding.Prefixes[0] = "198.51.100.0/24"
	if r.Privacy[0] == "changed" || r.Binding.Prefixes[0] == clone.Binding.Prefixes[0] {
		t.Fatal("review alias")
	}
	out.Outcome = "failed"
	out.FailureCode = "execution_failed"
	if validateHTTPSResult(out, r, now) != nil {
		t.Fatal("failed cleanup lost response")
	}
	out.Review = clone
	if validateHTTPSResult(out, r, now) == nil {
		t.Fatal("result swapped review")
	}
	out.Review = r
	m := out.Measurement
	m.StatusCode = 0
	m.ResponseTimeNanoseconds = nil
	m.Exchange = nq.HTTPSTimeout
	for _, stage := range []nq.HTTPSStage{nq.HTTPSConnect, nq.HTTPSTLS, nq.HTTPSRequest} {
		m.Stage = stage
		m.Request = nq.HTTPSRequestNotSent
		if stage == nq.HTTPSRequest {
			m.Request = nq.HTTPSRequestAccepted
		}
		out.Outcome = "completed"
		out.FailureCode = ""
		if validateHTTPSResult(out, r, now) != nil {
			t.Fatal("valid phase timeout rejected", stage)
		}
	}
	out.Outcome = "canceled"
	out.FailureCode = "canceled"
	m.StartedAt = r.ExpiresAt.Add(-time.Millisecond)
	m.CompletedAt = r.ExpiresAt.Add(time.Millisecond)
	m.Exchange = nq.HTTPSIncomplete
	if validateHTTPSResult(out, r, r.ExpiresAt.Add(time.Second)) != nil {
		t.Fatal("late incomplete cleanup rejected")
	}
	m.Exchange = nq.HTTPSTimeout
	if validateHTTPSResult(out, r, r.ExpiresAt.Add(time.Second)) == nil {
		t.Fatal("completed after review expired")
	}
	declined := api.HTTPSCheckResult{SchemaVersion: 1, Review: r, Outcome: "declined"}
	if validateHTTPSResult(declined, r, now) != nil {
		t.Fatal("decline rejected")
	}
	declined.RunID = sample.ID
	if validateHTTPSResult(declined, r, now) == nil {
		t.Fatal("decline retained run")
	}
	for _, err := range []error{httpsrun.ErrAudit, httpsrun.ErrClock, context.Canceled, context.DeadlineExceeded} {
		if code := httpsErrorCode(err); code == "unavailable" {
			t.Fatal("lost failure classification", err)
		}
	}
}

func TestHTTPSLongEscapedRequestFitsResponseFrame(t *testing.T) {
	r := httpsContractReview(t)
	r.Settings.RequestTarget = "/" + strings.Repeat("&", 1023)
	p, err := httpsplan.New(httpsplan.Binding{Observer: httpsWireObserver(r), Prefixes: r.Binding.Prefixes, Source: netip.MustParseAddr(r.Source)}, httpsplan.Configuration{Selection: httpsWireSelection(r), Endpoint: netip.MustParseAddrPort(r.Settings.Endpoint), ServerName: r.Settings.ServerName, RequestTarget: r.Settings.RequestTarget, DestinationPolicy: httpsplan.ExactEndpoint}, r.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	r = projectHTTPSReview(httpsrun.Review{Selection: httpsrun.Selection{Plan: p, RouteObservedAt: r.RouteObservedAt, RouteFreshUntil: r.RouteFreshUntil}, ExpiresAt: r.ExpiresAt}, r.Challenge)
	if validateHTTPSReview(r, r.SelectionID, r.CreatedAt) != nil {
		t.Fatal("long valid target rejected")
	}
	raw, err := json.Marshal(api.HTTPSCheckResult{SchemaVersion: 1, Review: r, RunID: strings.Repeat("f", 32), Outcome: "declined"})
	if err != nil || len(raw) <= gatewayFrameLimit || len(raw) > httpsFrameLimit-256 {
		t.Fatalf("unexpected disclosure size %d: %v", len(raw), err)
	}
}
