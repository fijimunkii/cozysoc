package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
)

func opnsenseCollectionRequest(t *testing.T, h *webHandler, route, body string, authorized bool) *httptest.ResponseRecorder {
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

func TestWebOPNsenseCollectionRequiresReviewAndConsumesDecision(t *testing.T) {
	h := newMutationTestHandler(t)
	statusReads, networkReads, collections := 0, 0, 0
	h.loadOPNsenseStatus = func(context.Context) (api.OPNsenseConnection, error) {
		statusReads++
		return api.OPNsenseConnection{Connected: true, Endpoint: "https://192.168.50.1", Version: opnsense.SupportedVersion}, nil
	}
	h.loadNetworks = func(context.Context) (api.NetworkList, error) {
		networkReads++
		return api.NetworkList{Enrolled: &api.EnrolledNetwork{ScopeID: "scope.home", Interface: api.NetworkInterface{
			InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}}}, nil
	}
	h.collectOPNsense = func(_ context.Context, params api.OPNsenseCollectParams) (api.OPNsenseCollection, error) {
		collections++
		if params.ScopeID != "scope.home" || params.Expected.Endpoint != "https://192.168.50.1" ||
			params.Expected.Interface.InterfaceName != "en0" || params.Expected.Interface.InterfaceIndex != 7 ||
			len(params.Expected.Interface.Prefixes) != 1 || params.Expected.Interface.Prefixes[0] != "192.168.50.0/24" {
			t.Fatalf("collection escaped reviewed binding: %+v", params)
		}
		return api.OPNsenseCollection{ScopeID: params.ScopeID, Read: 2, IPv4Total: 2, Inserted: 1, SkippedOutside: 1}, nil
	}
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/review", `{}`, false); rr.Code != http.StatusUnauthorized || statusReads != 0 {
		t.Fatalf("unauthorized review status=%d reads=%d", rr.Code, statusReads)
	}
	csrfMissing := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821/api/opnsense/collection/review", strings.NewReader(`{}`))
	csrfMissing.Header.Set("Origin", "http://127.0.0.1:43821")
	csrfMissing.Header.Set("Content-Type", "application/json")
	csrfResult := httptest.NewRecorder()
	h.ServeHTTP(csrfResult, csrfMissing)
	if csrfResult.Code < 400 || statusReads != 0 {
		t.Fatalf("CSRF-free review status=%d reads=%d", csrfResult.Code, statusReads)
	}
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/run", `{"review_id":"`+strings.Repeat("x", 43)+`","approve":true}`, true); rr.Code != http.StatusConflict || collections != 0 {
		t.Fatalf("unreviewed run status=%d collections=%d", rr.Code, collections)
	}
	rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/review", `{}`, true)
	if rr.Code != http.StatusOK || statusReads != 1 || networkReads != 1 {
		t.Fatalf("review status=%d body=%s", rr.Code, rr.Body.String())
	}
	var review webOPNsenseCollectionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil || review.ScopeID != "scope.home" || review.MaxRowsPerFamily != 256 || review.Endpoint != "https://192.168.50.1" {
		t.Fatalf("invalid review: %+v, %v", review, err)
	}
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/review", `{}`, true); rr.Code != http.StatusConflict || statusReads != 1 {
		t.Fatalf("overlapping review status=%d reads=%d", rr.Code, statusReads)
	}
	decision := `{"review_id":"` + review.ReviewID + `","approve":false}`
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/run", decision, true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"outcome":"declined"`) || collections != 0 {
		t.Fatalf("decline status=%d body=%s collections=%d", rr.Code, rr.Body.String(), collections)
	}
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/run", decision, true); rr.Code != http.StatusConflict || collections != 0 {
		t.Fatalf("replayed decision status=%d collections=%d", rr.Code, collections)
	}
	rr = opnsenseCollectionRequest(t, h, "/api/opnsense/collection/review", `{}`, true)
	if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	decision = `{"review_id":"` + review.ReviewID + `","approve":true}`
	rr = opnsenseCollectionRequest(t, h, "/api/opnsense/collection/run", decision, true)
	if rr.Code != http.StatusOK || collections != 1 || !strings.Contains(rr.Body.String(), `"inserted":1`) {
		t.Fatalf("approved run status=%d body=%s collections=%d", rr.Code, rr.Body.String(), collections)
	}
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/run", decision, true); rr.Code != http.StatusConflict || collections != 1 {
		t.Fatalf("replayed approval status=%d collections=%d", rr.Code, collections)
	}
	rr = opnsenseCollectionRequest(t, h, "/api/opnsense/collection/review", `{}`, true)
	if err := json.Unmarshal(rr.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	h.opnsenseReviewMu.Lock()
	h.opnsenseReview.expiresAt = time.Now().Add(-time.Second)
	h.opnsenseReviewMu.Unlock()
	if rr := opnsenseCollectionRequest(t, h, "/api/opnsense/collection/run", `{"review_id":"`+review.ReviewID+`","approve":true}`, true); rr.Code != http.StatusConflict || collections != 1 {
		t.Fatalf("expired approval status=%d collections=%d", rr.Code, collections)
	}
}

func TestWebOPNsenseCollectionReviewRequiresConnectionAndScope(t *testing.T) {
	h := newMutationTestHandler(t)
	status := api.OPNsenseConnection{Connected: false}
	h.loadOPNsenseStatus = func(context.Context) (api.OPNsenseConnection, error) { return status, nil }
	h.loadNetworks = func(context.Context) (api.NetworkList, error) { return api.NetworkList{}, nil }
	h.collectOPNsense = func(context.Context, api.OPNsenseCollectParams) (api.OPNsenseCollection, error) {
		t.Fatal("unexpected collection")
		return api.OPNsenseCollection{}, nil
	}
	request := func() *httptest.ResponseRecorder {
		return opnsenseCollectionRequest(t, h, "/api/opnsense/collection/review", `{}`, true)
	}
	if rr := request(); rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("disconnected status=%d", rr.Code)
	}
	status = api.OPNsenseConnection{Connected: true, Endpoint: "https://key:secret@192.168.50.1", Version: opnsense.SupportedVersion}
	if rr := request(); rr.Code != http.StatusPreconditionFailed || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("unsafe origin status=%d body=%s", rr.Code, rr.Body.String())
	}
	status.Endpoint = "https://192.168.50.1"
	if rr := request(); rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("missing scope status=%d", rr.Code)
	}
}

func TestWebOPNsenseCollectionRejectsMalformedCounts(t *testing.T) {
	valid := api.OPNsenseCollection{ScopeID: "scope.home", Read: 2, IPv4Total: 2, Inserted: 1, SkippedOutside: 1}
	if !validWebOPNsenseCollection(valid, "scope.home") {
		t.Fatal("valid result rejected")
	}
	truncated := api.OPNsenseCollection{ScopeID: "scope.home", Read: 256, IPv4Total: 257, IPv4Truncated: true, Inserted: 256}
	if !validWebOPNsenseCollection(truncated, "scope.home") {
		t.Fatal("bounded truncated result rejected")
	}
	for _, invalid := range []api.OPNsenseCollection{
		{ScopeID: "other", Read: 2, IPv4Total: 2, Inserted: 2},
		{ScopeID: "scope.home", Read: 3, IPv4Total: 2, Inserted: 3},
		{ScopeID: "scope.home", Read: 2, IPv4Total: 2, Inserted: 2, IPv4Truncated: true},
		{ScopeID: "scope.home", Read: 2, IPv4Total: 2, Inserted: 1},
	} {
		if validWebOPNsenseCollection(invalid, "scope.home") {
			t.Fatalf("malformed result accepted: %+v", invalid)
		}
	}
}
