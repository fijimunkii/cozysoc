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
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func httpsWebFixture(t *testing.T) api.HTTPSPlan {
	t.Helper()
	at := time.Now().UTC().Add(-5 * time.Second).Round(0)
	c := httpsplan.Configuration{Endpoint: netip.MustParseAddrPort("192.168.50.53:443"), ServerName: "private.example", RequestTarget: "/check?test=1", DestinationPolicy: httpsplan.ExactEndpoint,
		Selection: nq.HTTPSSelection{ID: "https-selection." + strings.Repeat("a", 32), EndpointID: "https-endpoint." + strings.Repeat("b", 32), RequestID: "https-request." + strings.Repeat("c", 32), Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}}
	p, err := httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: "scope.home", SensorID: "sensor.test", InterfaceName: "en0", InterfaceIndex: 4}, Prefixes: []string{"192.168.50.0/24"}, Source: netip.MustParseAddr("192.168.50.23")}, c, at)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Disclosure()
	request, err := p.RequestBytes()
	if err != nil {
		t.Fatal(err)
	}
	return api.HTTPSPlan{SchemaVersion: 1, Mode: "preview-only", Profile: d.Profile,
		Configuration: api.HTTPSSettings{SelectionID: c.Selection.ID, EndpointID: c.Selection.EndpointID, RequestID: c.Selection.RequestID, ScopeID: "scope.home", CreatedAt: at.Add(-time.Hour), Profile: httpsplan.Profile,
			Settings: api.HTTPSSettingsParams{Endpoint: c.Endpoint.String(), ServerName: c.ServerName, RequestTarget: c.RequestTarget, Family: "ipv4", Method: "HEAD", ExpectedStatus: 204, DestinationPolicy: httpsplan.ExactEndpoint}},
		Binding: api.GatewayPlanBinding{ScopeID: "scope.home", InterfaceName: "en0", InterfaceIndex: 4, Prefixes: []string{"192.168.50.0/24"}}, SensorID: "sensor.test", Source: "192.168.50.23", CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt,
		RouteObservedAt: at, RouteFreshUntil: at.Add(30 * time.Second), Policy: projectHTTPSPolicy(d.Policy), Budget: projectHTTPSBudget(d.Budget), RequestBytes: string(request), Privacy: d.Privacy}
}
func httpsWebReview(t *testing.T, h *webHandler, p api.HTTPSPlan) webHTTPSCheckReview {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/https/review", `{"selection_id":"`+p.Configuration.SelectionID+`"}`))
	if w.Code != 200 {
		t.Fatalf("review status %d: %s", w.Code, w.Body.String())
	}
	var r webHTTPSCheckReview
	if json.Unmarshal(w.Body.Bytes(), &r) != nil {
		t.Fatal("invalid review JSON")
	}
	return r
}
func nativeHTTPSWebReview(p api.HTTPSPlan) api.HTTPSCheckReview {
	created := p.CreatedAt.Add(time.Second)
	return api.HTTPSCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Profile: p.Profile, Challenge: strings.Repeat("a", 32), SelectionID: p.Configuration.SelectionID, EndpointID: p.Configuration.EndpointID, RequestID: p.Configuration.RequestID, Settings: p.Configuration.Settings, Binding: p.Binding, SensorID: p.SensorID, Source: p.Source, CreatedAt: created, ExpiresAt: created.Add(30 * time.Second), RouteObservedAt: created, RouteFreshUntil: created.Add(30 * time.Second), OutsideEnrolledPrefixes: p.OutsideEnrolledPrefixes, Policy: p.Policy, Budget: p.Budget, RequestBytes: p.RequestBytes, Privacy: p.Privacy}
}
func TestWebHTTPSNativeReviewAllowsShorterRouteExpiry(t *testing.T) {
	p := httpsWebFixture(t)
	r := nativeHTTPSWebReview(p)
	r.RouteObservedAt = p.RouteObservedAt
	r.RouteFreshUntil = p.RouteFreshUntil
	r.ExpiresAt = p.RouteFreshUntil
	if !httpsReviewMatchesPlan(r, p, time.Now().UTC()) {
		t.Fatal("fresh native route shortened the consent window")
	}
	r.ExpiresAt = r.CreatedAt.Add(httpsplan.ReviewLifetime)
	if httpsReviewMatchesPlan(r, p, time.Now().UTC()) {
		t.Fatal("extended consent beyond route freshness")
	}
}
func TestWebHTTPSExpiredRouteConsumesNoApproval(t *testing.T) {
	p := httpsWebFixture(t)
	h := newMutationTestHandler(t)
	h.loadHTTPSPlan = func(context.Context, string) (api.HTTPSPlan, error) { return p, nil }
	review := httpsWebReview(t, h, p)
	h.httpsReviewMu.Lock()
	h.httpsReview.plan.RouteFreshUntil = time.Now().Add(-time.Second)
	h.httpsReviewMu.Unlock()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/https/run", `{"review_id":"`+review.ReviewID+`","approve":true}`))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "review_expired") {
		t.Fatalf("expired route status %d: %s", w.Code, w.Body.String())
	}
}
func TestWebHTTPSSelectionsAndReviewBoundary(t *testing.T) {
	p := httpsWebFixture(t)
	h := newMutationTestHandler(t)
	h.loadHTTPSSettings = func(context.Context) (api.HTTPSSettingsResult, error) {
		return api.HTTPSSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.HTTPSSettings{p.Configuration}}, nil
	}
	h.loadHTTPSPlan = func(_ context.Context, id string) (api.HTTPSPlan, error) {
		if id != p.Configuration.SelectionID {
			t.Fatal(id)
		}
		return p, nil
	}
	get := authenticatedRequest("GET", "http://127.0.0.1:43821/api/network-quality/https/selections", nil)
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
		{http.MethodGet, "?selection_id=other", "", nil, 400}, {http.MethodGet, "?", "", nil, 400}, {http.MethodGet, "", "{}", nil, 400}, {http.MethodPost, "", "", nil, 405},
		{http.MethodGet, "", "", func(r *http.Request) { r.Header.Del("Cookie") }, 401}, {http.MethodGet, "", "", func(r *http.Request) { r.Host = "evil.test" }, 403},
	} {
		r := authenticatedRequest(tc.method, "http://127.0.0.1:43821/api/network-quality/https/selections"+tc.suffix, strings.NewReader(tc.body))
		if tc.modify != nil {
			tc.modify(r)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("selection read status %d: %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"selection_id":"bad"}`, `{"selection_id":"` + p.Configuration.SelectionID + `","approve":true}`, `{"selection_id":"` + p.Configuration.SelectionID + `"}{}`} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/https/review", body))
		if w.Code != 400 {
			t.Fatalf("body %q status %d", body, w.Code)
		}
	}
	for _, modify := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Origin") }, func(r *http.Request) { r.Header.Del(webCSRFHeader) }, func(r *http.Request) { r.Header.Del("Cookie") }} {
		r := gatewayWebRequest("/api/network-quality/https/review", `{"selection_id":"`+p.Configuration.SelectionID+`"}`)
		modify(r)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("boundary status %d", w.Code)
		}
	}
	r := httpsWebReview(t, h, p)
	if r.Settings.ServerName != "private.example" || r.Source != p.Source || r.RequestBytes != p.RequestBytes || r.Policy != p.Policy || r.Budget != p.Budget || len(r.Privacy) != 4 {
		t.Fatalf("wrong review: %+v", r)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/https/review", `{"selection_id":"`+p.Configuration.SelectionID+`"}`))
	if w.Code != 409 {
		t.Fatalf("pending review status %d", w.Code)
	}
	changed := p
	changed.Budget.MaxRequests = 2
	if _, err := projectWebHTTPSCheckReview(changed, p.Configuration.SelectionID, time.Now()); err == nil {
		t.Fatal("accepted modified budget")
	}
	changed = p
	changed.RequestBytes = "GET / HTTP/1.1\r\n"
	if _, err := projectWebHTTPSCheckReview(changed, p.Configuration.SelectionID, time.Now()); err == nil {
		t.Fatal("accepted modified request bytes")
	}
	changed = p
	changed.Privacy = []string{"short"}
	if _, err := projectWebHTTPSCheckReview(changed, p.Configuration.SelectionID, time.Now()); err == nil {
		t.Fatal("accepted modified privacy")
	}
}
func TestWebHTTPSDecisionOneUseChangedAndUnknown(t *testing.T) {
	for _, mode := range []string{"decline", "failed", "changed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			p := httpsWebFixture(t)
			h := newMutationTestHandler(t)
			h.loadHTTPSPlan = func(context.Context, string) (api.HTTPSPlan, error) { return p, nil }
			r := httpsWebReview(t, h, p)
			calls, approvals := 0, 0
			h.runHTTPSCheck = func(ctx context.Context, id string, decide func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error) {
				calls++
				if id != p.Configuration.SelectionID {
					t.Fatal(id)
				}
				native := nativeHTTPSWebReview(p)
				if mode == "changed" {
					native.RequestBytes = "GET / HTTP/1.1\r\n"
				}
				ok, err := decide(ctx, native)
				if ok {
					approvals++
				}
				if err != nil {
					return api.HTTPSCheckResult{}, err
				}
				if mode == "unknown" {
					return api.HTTPSCheckResult{}, errors.New("private diagnostic")
				}
				return api.HTTPSCheckResult{SchemaVersion: 1, Review: native, RunID: strings.Repeat("d", 32), Outcome: "failed", FailureCode: "execution_failed"}, nil
			}
			approve := mode != "decline"
			body := `{"review_id":"` + r.ReviewID + `","approve":` + map[bool]string{true: "true", false: "false"}[approve] + `}`
			w := httptest.NewRecorder()
			h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/https/run", body))
			want := map[string]int{"decline": 200, "failed": 200, "changed": 409, "unknown": 503}[mode]
			if w.Code != want || (mode == "decline" && calls != 0) || (mode == "changed" && approvals != 0) || (mode == "failed" && approvals != 1) || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("mode %s status %d calls %d approvals %d: %s", mode, w.Code, calls, approvals, w.Body.String())
			}
			if mode == "unknown" && !strings.Contains(w.Body.String(), "outcome_unknown") {
				t.Fatalf("missing unknown outcome: %s", w.Body.String())
			}
			again := httptest.NewRecorder()
			h.ServeHTTP(again, gatewayWebRequest("/api/network-quality/https/run", body))
			if again.Code != 409 {
				t.Fatalf("replay status %d", again.Code)
			}
		})
	}
}
