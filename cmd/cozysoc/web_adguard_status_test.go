package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestWebAdGuardStatusIsAuthenticatedExplicitAndCredentialFree(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	reads := 0
	h.loadAdGuardStatus = func(context.Context) (api.AdGuardConnection, error) {
		reads++
		return api.AdGuardConnection{Connected: true, Endpoint: "https://192.0.2.5:3000", Username: "private-reader", Version: adguard.SupportedVersion, Running: true, ProtectionEnabled: true, FilteringEnabled: true, QueryLogEnabled: true}, nil
	}
	request := func(method, target string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		req.Host = "127.0.0.1:9000"
		if authenticated {
			req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := request(http.MethodGet, "http://127.0.0.1:9000/api/adguard/status", false); rr.Code != http.StatusUnauthorized || reads != 0 {
		t.Fatalf("unauthorized status=%d reads=%d", rr.Code, reads)
	}
	if rr := request(http.MethodGet, "http://127.0.0.1:9000/api/adguard/status?scope=home", true); rr.Code != http.StatusBadRequest || reads != 0 {
		t.Fatalf("parameterized status=%d reads=%d", rr.Code, reads)
	}
	if rr := request(http.MethodPost, "http://127.0.0.1:9000/api/adguard/status", true); rr.Code != http.StatusMethodNotAllowed || reads != 0 {
		t.Fatalf("mutation status=%d reads=%d", rr.Code, reads)
	}
	rr := request(http.MethodGet, "http://127.0.0.1:9000/api/adguard/status", true)
	if rr.Code != http.StatusOK || reads != 1 || rr.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rr.Body.String(), `"endpoint":"https://192.0.2.5:3000"`) || strings.Contains(rr.Body.String(), "private-reader") {
		t.Fatalf("status=%d reads=%d body=%s", rr.Code, reads, rr.Body.String())
	}
}

func TestWebAdGuardStatusRejectsUnsafeOriginsAndRedactsFailures(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	h.loadAdGuardStatus = func(context.Context) (api.AdGuardConnection, error) {
		return api.AdGuardConnection{Connected: true, Endpoint: "http://192.0.2.5", Version: adguard.SupportedVersion}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/adguard/status", nil)
	req.Host = "127.0.0.1:9000"
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "192.0.2.5") {
		t.Fatalf("unsafe origin status=%d body=%s", rr.Code, rr.Body.String())
	}
	h.loadAdGuardStatus = func(context.Context) (api.AdGuardConnection, error) {
		return api.AdGuardConnection{}, errors.New("private credential diagnostic")
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private credential diagnostic") {
		t.Fatalf("error status=%d body=%s", rr.Code, rr.Body.String())
	}
}
