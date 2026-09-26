package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
)

func resolverWebFixture(t *testing.T) api.ResolverPlan {
	t.Helper()
	at := time.Now().UTC().Add(-5 * time.Second).Round(0)
	endpoint := netip.MustParseAddrPort("192.168.50.53:53")
	c := resolverplan.Configuration{Endpoint: endpoint, Name: "example.invalid.", DestinationScope: resolverplan.EnrolledPrefix,
		Selection: nq.ResolverSelection{ID: "selection." + strings.Repeat("a", 32), ResolverID: "resolver." + strings.Repeat("b", 32), QueryID: "query." + strings.Repeat("c", 32), Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}}
	p, err := resolverplan.New(resolverplan.Binding{Observer: nq.Observer{ScopeID: "scope.home", SensorID: "sensor.test", InterfaceName: "en0", InterfaceIndex: 4}, Prefixes: []string{"192.168.50.0/24"}, Source: netip.MustParseAddr("192.168.50.23")}, c, at)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Disclosure()
	b := d.Budget
	return api.ResolverPlan{SchemaVersion: 1, Mode: "preview-only", Profile: d.Profile,
		Configuration: api.ResolverSettings{SelectionID: c.Selection.ID, ResolverID: c.Selection.ResolverID, QueryID: c.Selection.QueryID, ScopeID: "scope.home", CreatedAt: at.Add(-time.Hour),
			Settings: api.ResolverSettingsParams{Endpoint: endpoint.String(), Name: c.Name, Family: "ipv4", Transport: "udp", QueryType: "A", Expect: "answer", DestinationScope: "enrolled-prefix"}},
		Binding: api.GatewayPlanBinding{ScopeID: "scope.home", InterfaceName: "en0", InterfaceIndex: 4, Prefixes: []string{"192.168.50.0/24"}}, SensorID: "sensor.test", Source: "192.168.50.23", CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt, MayForwardUpstream: true,
		Budget: api.ResolverPlanBudget{MaxSendCalls: b.MaxSendCalls, MaxRequestBytes: b.MaxRequestBytes, MaxReplyBytes: b.MaxReplyBytes, MaxReceivedDatagrams: b.MaxReceivedDatagrams, MaxReceiveCalls: b.MaxReceiveCalls, ExchangeTimeoutMS: b.ExchangeTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(), MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()}}
}
func resolverWebReview(t *testing.T, h *webHandler, p api.ResolverPlan) webResolverReview {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/resolver/review", `{"selection_id":"`+p.Configuration.SelectionID+`"}`))
	if w.Code != 200 {
		t.Fatalf("review status %d: %s", w.Code, w.Body.String())
	}
	var r webResolverReview
	if json.Unmarshal(w.Body.Bytes(), &r) != nil {
		t.Fatal("invalid review JSON")
	}
	return r
}
func nativeResolverWebReview(p api.ResolverPlan) api.ResolverCheckReview {
	created := p.CreatedAt.Add(time.Second)
	return api.ResolverCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Profile: p.Profile, Challenge: strings.Repeat("a", 32), SelectionID: p.Configuration.SelectionID, ResolverID: p.Configuration.ResolverID, QueryID: p.Configuration.QueryID, Settings: p.Configuration.Settings, Binding: p.Binding, SensorID: p.SensorID, Source: p.Source, CreatedAt: created, ExpiresAt: created.Add(20 * time.Second), OutsideEnrolledPrefixes: p.OutsideEnrolledPrefixes, MayForwardUpstream: p.MayForwardUpstream, Budget: p.Budget}
}
func TestWebResolverSelectionsAndReviewBoundary(t *testing.T) {
	p := resolverWebFixture(t)
	h := newMutationTestHandler(t)
	h.loadResolverSettings = func(context.Context) (api.ResolverSettingsResult, error) {
		return api.ResolverSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.ResolverSettings{p.Configuration}}, nil
	}
	h.loadResolverPlan = func(_ context.Context, id string) (api.ResolverPlan, error) {
		if id != p.Configuration.SelectionID {
			t.Fatal(id)
		}
		return p, nil
	}
	get := authenticatedRequest("GET", "http://127.0.0.1:43821/api/network-quality/resolver/selections", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, get)
	if w.Code != 200 || !strings.Contains(w.Body.String(), p.Configuration.SelectionID) || strings.Contains(w.Body.String(), "sensor.test") {
		t.Fatalf("selection status %d: %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		method, suffix, body string
		modify               func(*http.Request)
		want                 int
	}{
		{http.MethodGet, "?selection_id=other", "", nil, 400},
		{http.MethodGet, "?", "", nil, 400},
		{http.MethodGet, "", "{}", nil, 400},
		{http.MethodPost, "", "", nil, 405},
		{http.MethodGet, "", "", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
		{http.MethodGet, "", "", func(r *http.Request) { r.Host = "evil.test" }, 403},
		{http.MethodGet, "", "", func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, 403},
	} {
		r := authenticatedRequest(tc.method, "http://127.0.0.1:43821/api/network-quality/resolver/selections"+tc.suffix, strings.NewReader(tc.body))
		if tc.modify != nil {
			tc.modify(r)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("selection read %s %q status %d: %s", tc.method, tc.suffix, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"selection_id":"bad"}`, `{"selection_id":"` + p.Configuration.SelectionID + `","approve":true}`, `{"selection_id":"` + p.Configuration.SelectionID + `"}{}`} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/resolver/review", body))
		if w.Code != 400 {
			t.Fatalf("body %q status %d", body, w.Code)
		}
	}
	for _, modify := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Origin") }, func(r *http.Request) { r.Header.Del(webCSRFHeader) }, func(r *http.Request) { r.Header.Del("Cookie") }} {
		r := gatewayWebRequest("/api/network-quality/resolver/review", `{"selection_id":"`+p.Configuration.SelectionID+`"}`)
		modify(r)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("boundary status %d", w.Code)
		}
	}
	r := resolverWebReview(t, h, p)
	if r.Settings.Name != "example.invalid." || r.Source != p.Source || r.Budget != p.Budget || r.MayForwardUpstream != true || r.ReviewID == "" {
		t.Fatalf("wrong review: %+v", r)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/resolver/review", `{"selection_id":"`+p.Configuration.SelectionID+`"}`))
	if w.Code != 409 {
		t.Fatalf("pending review status %d", w.Code)
	}
	changed := p
	changed.Budget.MaxSendCalls = 2
	if _, err := projectWebResolverReview(changed, p.Configuration.SelectionID, time.Now()); err == nil {
		t.Fatal("accepted modified budget")
	}
	changed = p
	changed.Configuration.Settings.Name = "invalid"
	if _, err := projectWebResolverReview(changed, p.Configuration.SelectionID, time.Now()); err == nil {
		t.Fatal("accepted modified question")
	}
}
func TestWebResolverDecisionOneUseChangedAndUnknown(t *testing.T) {
	for _, mode := range []string{"decline", "completed", "changed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			p := resolverWebFixture(t)
			h := newMutationTestHandler(t)
			h.loadResolverPlan = func(context.Context, string) (api.ResolverPlan, error) { return p, nil }
			r := resolverWebReview(t, h, p)
			calls, approvals := 0, 0
			h.runResolverCheck = func(ctx context.Context, id string, decide func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error) {
				calls++
				if id != p.Configuration.SelectionID {
					t.Fatal(id)
				}
				native := nativeResolverWebReview(p)
				if mode == "changed" {
					native.Settings.Name = "other.invalid."
				}
				ok, err := decide(ctx, native)
				if ok {
					approvals++
				}
				if err != nil {
					return api.ResolverCheckResult{}, err
				}
				if mode == "unknown" {
					return api.ResolverCheckResult{}, errors.New("private diagnostic")
				}
				return api.ResolverCheckResult{SchemaVersion: 1, Review: native, RunID: strings.Repeat("d", 32), Outcome: "failed", FailureCode: "execution_failed"}, nil
			}
			approve := mode != "decline"
			body := `{"review_id":"` + r.ReviewID + `","approve":` + map[bool]string{true: "true", false: "false"}[approve] + `}`
			w := httptest.NewRecorder()
			h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/resolver/run", body))
			want := map[string]int{"decline": 200, "completed": 200, "changed": 409, "unknown": 503}[mode]
			if w.Code != want || (mode == "decline" && calls != 0) || (mode == "changed" && approvals != 0) || (mode == "completed" && approvals != 1) || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("mode %s status %d calls %d approvals %d: %s", mode, w.Code, calls, approvals, w.Body.String())
			}
			if mode == "unknown" && !strings.Contains(w.Body.String(), "outcome_unknown") {
				t.Fatalf("missing unknown outcome: %s", w.Body.String())
			}
			again := httptest.NewRecorder()
			h.ServeHTTP(again, gatewayWebRequest("/api/network-quality/resolver/run", body))
			if again.Code != 409 {
				t.Fatalf("replay status %d", again.Code)
			}
		})
	}
}
