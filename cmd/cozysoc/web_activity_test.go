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

func TestWebDeviceActivityIsAuthenticatedReadOnlyAndBounded(t *testing.T) {
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	handler.loadDeviceActivity = func(context.Context) (api.DeviceActivityList, error) {
		calls++
		now := time.Unix(1_800_000_000, 0).UTC()
		return api.DeviceActivityList{Configured: true, ScopeID: "scope.home", Since: now.Add(-24 * time.Hour), AsOf: now, Items: []api.DeviceActivityItem{{ID: "obs.one", Kind: "address-changed", At: now.Add(-time.Minute), DeviceID: "device.one", AddressFamily: "ipv4", Address: "192.168.1.20", PreviousAddress: "192.168.1.10", HardwareAddress: "02:00:00:00:00:01", Source: api.DeviceEvidenceSource{ObservationID: "obs.one", SensorID: "sensor.one", Kind: "device-neighbor-seen", SourceStream: "device-watch-neighbors", IngestedAt: now.Add(-time.Minute), Attribution: "device-watch:arp-cache"}}}}, nil
	}

	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/activity", nil))
	if unauth.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated activity status=%d calls=%d body=%s", unauth.Code, calls, unauth.Body.String())
	}
	for _, test := range []struct {
		method, url, body string
		want              int
	}{
		{http.MethodPost, "http://" + host + "/api/activity", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "http://" + host + "/api/activity?scope=other", "", http.StatusBadRequest},
		{http.MethodGet, "http://" + host + "/api/activity", "unexpected", http.StatusBadRequest},
	} {
		request := authenticatedRequest(test.method, test.url, strings.NewReader(test.body))
		if test.body == "" {
			request.Body = http.NoBody
			request.ContentLength = 0
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s status=%d want=%d body=%s", test.method, test.url, response.Code, test.want, response.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("rejected activity requests reached loader %d times", calls)
	}

	request := authenticatedRequest(http.MethodGet, "http://"+host+"/api/activity", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body.String(), `"kind":"address-changed"`) {
		t.Fatalf("activity response status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	for _, forbidden := range []string{"payload", "source_key", testBootstrapToken, testSessionToken} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("activity response exposed forbidden detail %q: %s", forbidden, response.Body.String())
		}
	}
}
