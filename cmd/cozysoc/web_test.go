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

func TestWebHandlerCoverageSecurityBoundary(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, uiDir, func(context.Context) (coverageEnvelope, error) {
		calls++
		return testCoverageEnvelope(), nil
	})

	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/coverage", nil)
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
	for _, forbidden := range []string{"controller.auth", "session_secret", "operational", "neighbors_in_scope", "blind_spots"} {
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
	handler := newWebHandler(host, uiDir, func(context.Context) (coverageEnvelope, error) {
		calls++
		return testCoverageEnvelope(), nil
	})

	tests := []struct {
		name   string
		method string
		url    string
		host   string
		origin string
		body   io.Reader
		want   int
	}{
		{name: "wrong host", method: http.MethodGet, url: "http://" + host + "/api/coverage", host: "attacker.invalid", want: http.StatusForbidden},
		{name: "wrong origin", method: http.MethodGet, url: "http://" + host + "/api/coverage", origin: "https://attacker.invalid", want: http.StatusForbidden},
		{name: "mutation method", method: http.MethodPost, url: "http://" + host + "/api/coverage", want: http.StatusMethodNotAllowed},
		{name: "query parameters", method: http.MethodGet, url: "http://" + host + "/api/coverage?scope=other", want: http.StatusBadRequest},
		{name: "get body", method: http.MethodGet, url: "http://" + host + "/api/coverage", body: strings.NewReader("unexpected"), want: http.StatusBadRequest},
		{name: "generic rpc absent", method: http.MethodPost, url: "http://" + host + "/api/call", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, tt.body)
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
	handler := newWebHandler(host, uiDir, func(context.Context) (coverageEnvelope, error) {
		return coverageEnvelope{}, errors.New("offline")
	})
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/coverage", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"error":"controller_unavailable"`) {
		t.Fatalf("unexpected unavailable response: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestWebHandlerServesOnlyRegularStaticFiles(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, func(context.Context) (coverageEnvelope, error) {
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
}

func TestWebHandlerRejectsOversizedRequestURI(t *testing.T) {
	uiDir := testUIDir(t)
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, uiDir, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/"+strings.Repeat("a", maxWebRequestURI+1), nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestURITooLong {
		t.Fatalf("oversized URI status = %d, want %d", res.Code, http.StatusRequestURITooLong)
	}
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
