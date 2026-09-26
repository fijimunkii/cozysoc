package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestWebOPNsenseNeighborsIsAuthenticatedExplicitAndRedactedOnFailure(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	reads := 0
	h.loadOPNsenseNeighbors = func(context.Context) (api.OPNsenseNeighborHistory, error) {
		reads++
		return api.OPNsenseNeighborHistory{ScopeEnrolled: true, ScopeID: "scope.home", AsOf: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC),
			Reports: []api.OPNsenseNeighborReport{{ObservationID: "obs.opnsense.one", Address: "192.168.1.8", Hardware: "02:00:00:00:00:08", Interface: "lan", Family: "ipv4"}}}, nil
	}
	request := func(method, target string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	path := "http://127.0.0.1:9000/api/opnsense/neighbors"
	if rr := request(http.MethodGet, path, false); rr.Code != http.StatusUnauthorized || reads != 0 {
		t.Fatalf("unauthorized status=%d reads=%d", rr.Code, reads)
	}
	if rr := request(http.MethodPost, path, true); rr.Code != http.StatusMethodNotAllowed || reads != 0 {
		t.Fatalf("mutation status=%d reads=%d", rr.Code, reads)
	}
	if rr := request(http.MethodGet, path+"?scope=other", true); rr.Code != http.StatusBadRequest || reads != 0 {
		t.Fatalf("parameterized status=%d reads=%d", rr.Code, reads)
	}
	rr := request(http.MethodGet, path, true)
	if rr.Code != http.StatusOK || reads != 1 || rr.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rr.Body.String(), "192.168.1.8") {
		t.Fatalf("status=%d reads=%d body=%s", rr.Code, reads, rr.Body.String())
	}
	h.loadOPNsenseNeighbors = func(context.Context) (api.OPNsenseNeighborHistory, error) {
		return api.OPNsenseNeighborHistory{}, errors.New("private router detail")
	}
	rr = request(http.MethodGet, path, true)
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private router detail") {
		t.Fatalf("failure status=%d body=%s", rr.Code, rr.Body.String())
	}
}
