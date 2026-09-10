package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

const (
	testBootstrapToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testSessionToken   = "sssssssssssssssssssssssssssssssssssssssssss"
)

func TestValidateLoopbackListen(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:43821", "[::1]:0"} {
		if err := validateLoopbackListen(address); err != nil {
			t.Fatalf("expected %q to be accepted: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:43821", "192.168.1.10:43821", "localhost:43821", ":43821", "127.0.0.1:99999"} {
		if err := validateLoopbackListen(address); err == nil {
			t.Fatalf("expected %q to be rejected", address)
		}
	}
}

func TestWebHandlerSessionBootstrapIsOneTimeAndHttpOnly(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})

	exchange := func(token, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/session", strings.NewReader(`{"bootstrap":"`+token+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	wrongOrigin := exchange(testBootstrapToken, "https://attacker.invalid")
	if wrongOrigin.Code != http.StatusForbidden {
		t.Fatalf("wrong-origin bootstrap status = %d, want 403", wrongOrigin.Code)
	}
	wrongToken := exchange(strings.Repeat("x", len(testBootstrapToken)), "http://"+host)
	if wrongToken.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-token bootstrap status = %d, want 401", wrongToken.Code)
	}

	first := exchange(testBootstrapToken, "http://"+host)
	if first.Code != http.StatusNoContent {
		t.Fatalf("bootstrap status = %d, body=%s", first.Code, first.Body.String())
	}
	cookies := first.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bootstrap cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != webSessionCookie || cookie.Value != testSessionToken || cookie.Value == testBootstrapToken {
		t.Fatalf("unexpected browser session cookie: %+v", cookie)
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("browser session cookie flags are too weak: %+v", cookie)
	}
	if got := first.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("bootstrap Cache-Control = %q, want no-store", got)
	}

	second := exchange(testBootstrapToken, "http://"+host)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reused bootstrap status = %d, want 401", second.Code)
	}
}

func TestWebHandlerCoverageRequiresWebSession(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		calls++
		return testCoverageEnvelope(), nil
	})
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/coverage", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), `"error":"web_session_required"`) {
		t.Fatalf("unauthenticated coverage response: status=%d body=%s", res.Code, res.Body.String())
	}
	if calls != 0 {
		t.Fatalf("unauthenticated coverage reached loader %d times", calls)
	}
}

func TestWebHandlerCoverageSecurityBoundary(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		calls++
		return testCoverageEnvelope(), nil
	})

	req := authenticatedRequest(http.MethodGet, "http://"+host+"/api/coverage", nil)
	req.Header.Set("Origin", "http://"+host)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("coverage status = %d, body=%s", res.Code, res.Body.String())
	}
	if calls != 1 {
		t.Fatalf("coverage loader calls = %d, want 1", calls)
	}
	body := res.Body.String()
	for _, forbidden := range []string{"controller.auth", "session_secret", "operational", "neighbors_in_scope", "blind_spots", testBootstrapToken, testSessionToken} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("coverage response exposed forbidden detail %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"capability_id":"device-watch"`) || !strings.Contains(body, `"reports"`) {
		t.Fatalf("coverage response did not expose shared report: %s", body)
	}
	for _, header := range []string{"Content-Security-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy", "Referrer-Policy", "X-Content-Type-Options"} {
		if res.Header().Get(header) == "" {
			t.Fatalf("missing security header %s", header)
		}
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("coverage Cache-Control = %q, want no-store", got)
	}
}

func TestWebHandlerRejectsInvalidBrowserRequests(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		calls++
		return testCoverageEnvelope(), nil
	})

	tests := []struct {
		name          string
		method        string
		url           string
		host          string
		origin        string
		body          io.Reader
		authenticated bool
		want          int
	}{
		{name: "wrong host", method: http.MethodGet, url: "http://" + host + "/api/coverage", host: "attacker.invalid", authenticated: true, want: http.StatusForbidden},
		{name: "wrong origin", method: http.MethodGet, url: "http://" + host + "/api/coverage", origin: "https://attacker.invalid", authenticated: true, want: http.StatusForbidden},
		{name: "mutation method", method: http.MethodPost, url: "http://" + host + "/api/coverage", authenticated: true, want: http.StatusMethodNotAllowed},
		{name: "query parameters", method: http.MethodGet, url: "http://" + host + "/api/coverage?scope=other", authenticated: true, want: http.StatusBadRequest},
		{name: "get body", method: http.MethodGet, url: "http://" + host + "/api/coverage", body: strings.NewReader("unexpected"), authenticated: true, want: http.StatusBadRequest},
		{name: "generic rpc absent", method: http.MethodPost, url: "http://" + host + "/api/call", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.authenticated {
				req = authenticatedRequest(tt.method, tt.url, tt.body)
			} else {
				req = httptest.NewRequest(tt.method, tt.url, tt.body)
			}
			if tt.host != "" {
				req.Host = tt.host
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", res.Code, tt.want, res.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected requests reached coverage loader %d times", calls)
	}
}

func TestWebHandlerControllerUnavailable(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return coverageEnvelope{}, errors.New("offline")
	})
	req := authenticatedRequest(http.MethodGet, "http://"+host+"/api/coverage", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"error":"controller_unavailable"`) {
		t.Fatalf("unexpected unavailable response: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestWebHandlerServesOnlyRegularStaticFilesInsideRoot(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("unexpected root response: status=%d body=%s", root.Code, root.Body.String())
	}

	directory := httptest.NewRecorder()
	handler.ServeHTTP(directory, httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/", nil))
	if directory.Code != http.StatusNotFound {
		t.Fatalf("directory listing status = %d, want 404", directory.Code)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(uiDir, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}
	escape := httptest.NewRecorder()
	handler.ServeHTTP(escape, httptest.NewRequest(http.MethodGet, "http://"+host+"/escape.txt", nil))
	if escape.Code != http.StatusNotFound || strings.Contains(escape.Body.String(), "outside") {
		t.Fatalf("static server escaped UI root: status=%d body=%s", escape.Code, escape.Body.String())
	}
}

func TestWebHandlerRejectsOversizedRequestURI(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/"+strings.Repeat("a", maxWebRequestURI+1), nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestURITooLong {
		t.Fatalf("oversized URI status = %d, want %d", res.Code, http.StatusRequestURITooLong)
	}
}

func authenticatedRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: testSessionToken})
	return req
}

func testUIDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<!doctype html><div id="root"></div>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testCoverageEnvelope() coverageEnvelope {
	return coverageEnvelope{
		AsOf: time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC),
		Reports: []api.CoverageReport{{
			CapabilityID:      "device-watch",
			Configured:        false,
			State:             "unconfigured",
			Reason:            "not-configured",
			ObservationPoints: []api.CoverageObservationPoint{},
			NextStep:          "Enroll a home network before enabling Device Watch.",
		}},
	}
}

func TestWebHandlerDevicesReadBoundary(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})
	handler.loadDevices = func(context.Context) (api.DeviceList, error) {
		calls++
		return testDeviceList(), nil
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/devices", nil))
	if unauthenticated.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated devices response: status=%d calls=%d", unauthenticated.Code, calls)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/devices", nil))
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("devices response: status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{testBootstrapToken, testSessionToken, "controller.auth", "session_secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("devices response exposed forbidden detail %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"user_label":"Living Room TV"`) || !strings.Contains(body, `"state":"visible"`) {
		t.Fatalf("devices response did not expose bounded presence data: %s", body)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rejected := httptest.NewRecorder()
		handler.ServeHTTP(rejected, authenticatedRequest(method, "http://"+host+"/api/devices", nil))
		if rejected.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s devices status = %d, want 405", method, rejected.Code)
		}
	}
}

func TestWebHandlerDevicesControllerUnavailable(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})
	handler.loadDevices = func(context.Context) (api.DeviceList, error) {
		return api.DeviceList{}, errors.New("offline")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/devices", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"error":"controller_unavailable"`) {
		t.Fatalf("unexpected unavailable devices response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func testDeviceList() api.DeviceList {
	return api.DeviceList{
		Configured: true,
		ScopeID:    "scope.home",
		AsOf:       time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC),
		Devices: []api.DevicePresence{{
			ID:        "device.one",
			UserLabel: "Living Room TV",
			FirstSeen: time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC),
			LastSeen:  time.Date(2026, 9, 10, 0, 59, 0, 0, time.UTC),
			State:     "visible",
		}},
		Truncated: false,
	}
}
