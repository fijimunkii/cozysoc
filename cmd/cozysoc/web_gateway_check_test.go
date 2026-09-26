package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func gatewayWebFixture(t *testing.T) api.GatewayCheckPlan {
	t.Helper()
	now := time.Now().UTC().Add(-6 * time.Second).Round(0)
	canonical, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{
		ScopeID: "scope.home", InterfaceName: "en0", InterfaceIndex: 4, Prefixes: []string{"192.168.50.0/24"},
	}, "192.168.50.1", now)
	if err != nil {
		t.Fatal(err)
	}
	plan := projectGatewayCheckPlan(canonical)
	plan.Route = api.GatewayRouteReview{State: "consistent", Source: "darwin-rtm-get", SourceAddress: "192.168.50.23", ObservedAt: &now, FreshUntil: &plan.ReviewExpiresAt}
	return plan
}

func gatewayWebRequest(path, body string) *http.Request {
	r := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://127.0.0.1:43821")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(webCSRFHeader, testCSRFToken)
	return r
}

func gatewayWebReview(t *testing.T, h *webHandler) webGatewayReview {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/gateway/review", `{"target":"192.168.50.1"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("review status %d: %s", w.Code, w.Body.String())
	}
	var review webGatewayReview
	if err := json.Unmarshal(w.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	return review
}

func nativeWebReview(plan api.GatewayCheckPlan) api.GatewayCheckReview {
	created := plan.CreatedAt.Add(time.Second)
	return api.GatewayCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Profile: gatewayrun.Profile,
		CreatedAt: created, ExpiresAt: created.Add(networkquality.GatewayReviewLifetime), Binding: plan.Binding,
		Target: plan.Target.Address, Source: plan.Route.SourceAddress, Budget: plan.ProposedBudget}
}

func TestWebGatewayReviewRequiresMutationBoundaryAndCanonicalPlan(t *testing.T) {
	h := newMutationTestHandler(t)
	plan := gatewayWebFixture(t)
	calls := 0
	h.loadGatewayPlan = func(_ context.Context, target string) (api.GatewayCheckPlan, error) {
		calls++
		if target != plan.Target.Address {
			t.Fatal(target)
		}
		return plan, nil
	}
	path := "/api/network-quality/gateway/review"
	for _, modify := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Origin") },
		func(r *http.Request) { r.Header.Del(webCSRFHeader) },
		func(r *http.Request) { r.Header.Del("Cookie") },
	} {
		r := gatewayWebRequest(path, `{"target":"192.168.50.1"}`)
		modify(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized {
			t.Fatalf("boundary status %d", w.Code)
		}
	}
	for _, body := range []string{`{"target":"example.com"}`, `{"target":"8.8.8.8"}`, `{"target":"192.168.50.1","approve":true}`, `{"target":"192.168.50.1"}{}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, gatewayWebRequest(path, body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %q status %d", body, w.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("rejected requests reached controller %d times", calls)
	}
	review := gatewayWebReview(t, h)
	if !webGatewayReviewIDPattern.MatchString(review.ReviewID) || review.Target != plan.Target.Address || review.Source != plan.Route.SourceAddress || review.Budget != plan.ProposedBudget {
		t.Fatalf("review mismatch %+v", review)
	}
	if strings.Contains(review.ReviewID, plan.Binding.ScopeID) {
		t.Fatal("review ID contains scope")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest(path, `{"target":"192.168.50.1"}`))
	if w.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("pending review status %d calls %d", w.Code, calls)
	}
}

func TestWebGatewayDecisionIsOneUseAndRevalidatesNativeReview(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "approved", true: "changed"}[changed], func(t *testing.T) {
			h := newMutationTestHandler(t)
			plan := gatewayWebFixture(t)
			h.loadGatewayPlan = func(context.Context, string) (api.GatewayCheckPlan, error) { return plan, nil }
			review := gatewayWebReview(t, h)
			calls, approvals := 0, 0
			h.runGatewayCheck = func(ctx context.Context, target string, decide func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
				calls++
				if target != plan.Target.Address {
					t.Fatal(target)
				}
				native := nativeWebReview(plan)
				if changed {
					native.Source = "192.168.50.24"
				}
				ok, err := decide(ctx, native)
				if ok {
					approvals++
				}
				if err != nil {
					return api.GatewayCheckResult{}, err
				}
				completedAt := native.CreatedAt.Add(4 * time.Second)
				rtt := int64(100_000_000)
				return api.GatewayCheckResult{SchemaVersion: 1, Review: native, RunID: strings.Repeat("a", 32), Outcome: "completed",
					Measurement: &api.GatewayRunMeasurement{StartedAt: native.CreatedAt.Add(time.Second), CompletedAt: &completedAt,
						SendCalls: 3, AcceptedRequests: 3, Replies: 2, Timeouts: 1, Complete: true, MeanRTTNanoseconds: &rtt}}, nil
			}
			body := `{"review_id":"` + review.ReviewID + `","approve":true}`
			w := httptest.NewRecorder()
			h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/gateway/run", body))
			if changed {
				if w.Code != http.StatusConflict || approvals != 0 {
					t.Fatalf("changed status %d approvals %d: %s", w.Code, approvals, w.Body.String())
				}
			} else if w.Code != http.StatusOK || approvals != 1 || !strings.Contains(w.Body.String(), `"outcome":"completed"`) || !strings.Contains(w.Body.String(), `"replies":2`) {
				t.Fatalf("approved status %d approvals %d: %s", w.Code, approvals, w.Body.String())
			}
			again := httptest.NewRecorder()
			h.ServeHTTP(again, gatewayWebRequest("/api/network-quality/gateway/run", body))
			if again.Code != http.StatusConflict || calls != 1 {
				t.Fatalf("replay status %d calls %d", again.Code, calls)
			}
		})
	}
}

func TestWebGatewayDeclineExpiryAndUnknownOutcome(t *testing.T) {
	h := newMutationTestHandler(t)
	plan := gatewayWebFixture(t)
	h.loadGatewayPlan = func(context.Context, string) (api.GatewayCheckPlan, error) { return plan, nil }
	calls := 0
	h.runGatewayCheck = func(ctx context.Context, _ string, decide func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
		calls++
		ok, err := decide(ctx, nativeWebReview(plan))
		if err != nil || !ok {
			t.Fatalf("unexpected decision %t %v", ok, err)
		}
		return api.GatewayCheckResult{}, errors.New("private controller failure")
	}
	review := gatewayWebReview(t, h)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/gateway/run", `{"review_id":"`+review.ReviewID+`","approve":false}`))
	if w.Code != http.StatusOK || calls != 0 || !strings.Contains(w.Body.String(), `"outcome":"declined"`) {
		t.Fatalf("decline status %d calls %d: %s", w.Code, calls, w.Body.String())
	}
	review = gatewayWebReview(t, h)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/gateway/run", `{"review_id":"`+review.ReviewID+`","approve":true}`))
	if w.Code != http.StatusServiceUnavailable || calls != 1 || !strings.Contains(w.Body.String(), `"error":"outcome_unknown"`) || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("unknown status %d calls %d: %s", w.Code, calls, w.Body.String())
	}
	review = gatewayWebReview(t, h)
	h.gatewayReviewMu.Lock()
	h.gatewayReview.plan.ReviewExpiresAt = time.Now().Add(-time.Second)
	h.gatewayReviewMu.Unlock()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/gateway/run", `{"review_id":"`+review.ReviewID+`","approve":true}`))
	if w.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("expired status %d calls %d", w.Code, calls)
	}
}
