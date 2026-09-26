package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
)

func TestWebOPNsenseStatusRequiresSessionAndExplicitRead(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	reads := 0
	h.loadOPNsenseStatus = func(context.Context) (api.OPNsenseConnection, error) {
		reads++
		return api.OPNsenseConnection{Connected: true, Endpoint: "https://192.168.1.1:8443", Version: opnsense.SupportedVersion}, nil
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
	path := "http://127.0.0.1:9000/api/opnsense/status"
	if rr := request(http.MethodGet, path, false); rr.Code != http.StatusUnauthorized || reads != 0 {
		t.Fatalf("unauthorized status=%d reads=%d", rr.Code, reads)
	}
	if rr := request(http.MethodPost, path, true); rr.Code != http.StatusMethodNotAllowed || reads != 0 {
		t.Fatalf("mutation status=%d reads=%d", rr.Code, reads)
	}
	if rr := request(http.MethodGet, path+"?detail=1", true); rr.Code != http.StatusBadRequest || reads != 0 {
		t.Fatalf("parameterized status=%d reads=%d", rr.Code, reads)
	}
	rr := request(http.MethodGet, path, true)
	if rr.Code != http.StatusOK || reads != 1 || rr.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(rr.Body.String(), `"endpoint":"https://192.168.1.1:8443"`) {
		t.Fatalf("status=%d reads=%d body=%s", rr.Code, reads, rr.Body.String())
	}
}

func TestWebOPNsenseStatusRejectsUnsafeOriginAndRedactsFailures(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/opnsense/status", nil)
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	h.loadOPNsenseStatus = func(context.Context) (api.OPNsenseConnection, error) {
		return api.OPNsenseConnection{Connected: true, Endpoint: "https://key:secret@192.168.1.1", Version: opnsense.SupportedVersion}, nil
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("unsafe endpoint status=%d body=%s", rr.Code, rr.Body.String())
	}
	h.loadOPNsenseStatus = func(context.Context) (api.OPNsenseConnection, error) {
		return api.OPNsenseConnection{}, errors.New("private credential diagnostic")
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private credential diagnostic") {
		t.Fatalf("failure status=%d body=%s", rr.Code, rr.Body.String())
	}
}
