package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestWebArrivalFindingsRequireSessionAndReturnOnlyProjection(t *testing.T) {
	const host = "127.0.0.1:43821"
	h := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	calls := 0
	h.loadArrivalFindings = func(context.Context) (api.ArrivalFindingList, error) {
		calls++
		at := time.Unix(1_800_000_000, 0).UTC()
		return api.ArrivalFindingList{AsOf: at, Items: []api.ArrivalFindingItem{{ID: "finding.one", ScopeID: "scope.home", ObservedAt: at, RecordedAt: at, EvidenceObservationID: "obs.one", EvidenceRetained: false}}}, nil
	}
	for _, tc := range []struct {
		method, path string
		auth         bool
		want         int
	}{
		{http.MethodGet, "/api/findings/arrivals", false, http.StatusUnauthorized},
		{http.MethodPost, "/api/findings/arrivals", true, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/findings/arrivals?scope=other", true, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(tc.method, "http://"+host+tc.path, nil)
		if tc.auth {
			request = authenticatedRequest(tc.method, "http://"+host+tc.path, nil)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != tc.want || calls != 0 {
			t.Fatal(response.Code, tc.want, calls)
		}
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/findings/arrivals", nil))
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body.String(), `"evidence_retained":false`) {
		t.Fatal(response.Code, calls, response.Body.String())
	}
	for _, forbidden := range []string{"payload", "private", testBootstrapToken, testSessionToken} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatal("private data in finding response", forbidden)
		}
	}
}
