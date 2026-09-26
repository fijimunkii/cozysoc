package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestWebAdGuardCollectionRequiresReviewAndConsumesDecision(t *testing.T) {
	h := newMutationTestHandler(t)
	statusReads, networkReads, collections := 0, 0, 0
	h.loadAdGuardStatus = func(context.Context) (api.AdGuardConnection, error) {
		statusReads++
		return api.AdGuardConnection{Connected: true, Endpoint: "https://192.0.2.5:3000", Username: "private-user",
			Version: adguard.SupportedVersion, Running: true, QueryLogEnabled: true}, nil
	}
	h.loadNetworks = func(context.Context) (api.NetworkList, error) {
		networkReads++
		return api.NetworkList{Enrolled: &api.EnrolledNetwork{ScopeID: "scope.home", Interface: api.NetworkInterface{
			InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.0.2.0/24"}}}}, nil
	}
	h.collectAdGuard = func(_ context.Context, params api.AdGuardCollectParams) (api.AdGuardCollection, error) {
		collections++
		if params.ScopeID != "scope.home" || params.Expected == nil || params.Expected.Endpoint != "https://192.0.2.5:3000" ||
			params.Expected.Interface.InterfaceName != "en0" || params.Expected.Interface.InterfaceIndex != 7 ||
			len(params.Expected.Interface.Prefixes) != 1 || params.Expected.Interface.Prefixes[0] != "192.0.2.0/24" {
			t.Fatalf("collection escaped reviewed binding: %+v", params)
		}
		return api.AdGuardCollection{ScopeID: params.ScopeID, Read: 2, Inserted: 1, SkippedOutsideScope: 1}, nil
	}
	request := func(route, body string, authorized bool) *httptest.ResponseRecorder {
		t.Helper()
		url := "http://127.0.0.1:43821" + route
		var req *http.Request
		if authorized {
			req = authenticatedRequest(http.MethodPost, url, strings.NewReader(body))
			req.Header.Set("Origin", "http://127.0.0.1:43821")
			req.Header.Set(webCSRFHeader, testCSRFToken)
		} else {
			req = httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		}
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := request("/api/adguard/collection/review", `{}`, false); rr.Code != http.StatusUnauthorized || statusReads != 0 {
		t.Fatalf("unauthorized review status=%d reads=%d", rr.Code, statusReads)
	}
	if rr := request("/api/adguard/collection/run", `{"review_id":"`+strings.Repeat("x", 43)+`","approve":true}`, true); rr.Code != http.StatusConflict || collections != 0 {
		t.Fatalf("unreviewed run status=%d collections=%d", rr.Code, collections)
	}
	rr := request("/api/adguard/collection/review", `{}`, true)
	if rr.Code != http.StatusOK || statusReads != 1 || networkReads != 1 || strings.Contains(rr.Body.String(), "private-user") {
		t.Fatalf("review status=%d body=%s", rr.Code, rr.Body.String())
	}
	var review webAdGuardCollectionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil || review.ScopeID != "scope.home" || review.MaxQueries != 100 || review.MaxQueryAgeHours != 24 {
		t.Fatalf("invalid review: %+v, %v", review, err)
	}
	if rr := request("/api/adguard/collection/review", `{}`, true); rr.Code != http.StatusConflict || statusReads != 1 {
		t.Fatalf("overlapping review status=%d reads=%d", rr.Code, statusReads)
	}
	decision := `{"review_id":"` + review.ReviewID + `","approve":false}`
	if rr := request("/api/adguard/collection/run", decision, true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"outcome":"declined"`) || collections != 0 {
		t.Fatalf("decline status=%d body=%s collections=%d", rr.Code, rr.Body.String(), collections)
	}
	if rr := request("/api/adguard/collection/run", decision, true); rr.Code != http.StatusConflict || collections != 0 {
		t.Fatalf("replayed decision status=%d collections=%d", rr.Code, collections)
	}
	rr = request("/api/adguard/collection/review", `{}`, true)
	if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	decision = `{"review_id":"` + review.ReviewID + `","approve":true}`
	rr = request("/api/adguard/collection/run", decision, true)
	if rr.Code != http.StatusOK || collections != 1 || !strings.Contains(rr.Body.String(), `"inserted":1`) {
		t.Fatalf("approved run status=%d body=%s collections=%d", rr.Code, rr.Body.String(), collections)
	}
	if rr := request("/api/adguard/collection/run", decision, true); rr.Code != http.StatusConflict || collections != 1 {
		t.Fatalf("replayed approval status=%d collections=%d", rr.Code, collections)
	}
	// An expired approval fails before any controller call.
	rr = request("/api/adguard/collection/review", `{}`, true)
	if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	h.adguardReviewMu.Lock()
	h.adguardReview.expiresAt = time.Now().Add(-time.Second)
	h.adguardReviewMu.Unlock()
	if rr := request("/api/adguard/collection/run", `{"review_id":"`+review.ReviewID+`","approve":true}`, true); rr.Code != http.StatusConflict || collections != 1 {
		t.Fatalf("expired approval status=%d collections=%d", rr.Code, collections)
	}
}

func TestWebAdGuardCollectionReviewRequiresActiveQueryLogAndScope(t *testing.T) {
	h := newMutationTestHandler(t)
	status := api.AdGuardConnection{Connected: true, Endpoint: "https://192.0.2.5:3000", Version: adguard.SupportedVersion, Running: true}
	h.loadAdGuardStatus = func(context.Context) (api.AdGuardConnection, error) { return status, nil }
	h.loadNetworks = func(context.Context) (api.NetworkList, error) { return api.NetworkList{}, nil }
	h.collectAdGuard = func(context.Context, api.AdGuardCollectParams) (api.AdGuardCollection, error) {
		t.Fatal("unexpected collection")
		return api.AdGuardCollection{}, nil
	}
	request := func() *httptest.ResponseRecorder {
		req := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821/api/adguard/collection/review", strings.NewReader(`{}`))
		req.Header.Set("Origin", "http://127.0.0.1:43821")
		req.Header.Set(webCSRFHeader, testCSRFToken)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := request(); rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("disabled query log status=%d", rr.Code)
	}
	status.QueryLogEnabled = true
	if rr := request(); rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("missing scope status=%d", rr.Code)
	}
}
