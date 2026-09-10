package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func TestWebDeviceDetailIsAuthenticatedAndScopeFree(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	var got string
	h.loadDeviceDetail = func(_ context.Context, deviceID string) (api.DeviceDetail, error) {
		got = deviceID
		return api.DeviceDetail{ScopeID: "scope.home", AsOf: time.Unix(2, 0).UTC(), Device: api.DevicePresence{ID: deviceID, FirstSeen: time.Unix(1, 0).UTC(), LastSeen: time.Unix(2, 0).UTC(), State: "visible"}, Evidence: []api.DeviceIdentityEvidence{}}, nil
	}

	unauth := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/devices/detail?device_id=device.one", nil)
	unauth.Host = "127.0.0.1:9000"
	unauthRR := httptest.NewRecorder()
	h.ServeHTTP(unauthRR, unauth)
	if unauthRR.Code != http.StatusUnauthorized || got != "" {
		t.Fatalf("unauth status=%d got=%q", unauthRR.Code, got)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/devices/detail?device_id=device.one", nil)
	req.Host = "127.0.0.1:9000"
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || got != "device.one" || !strings.Contains(rr.Body.String(), `"scope_id":"scope.home"`) {
		t.Fatalf("status=%d got=%q body=%s", rr.Code, got, rr.Body.String())
	}

	for _, raw := range []string{
		"http://127.0.0.1:9000/api/devices/detail",
		"http://127.0.0.1:9000/api/devices/detail?device_id=../bad",
		"http://127.0.0.1:9000/api/devices/detail?device_id=device.one&scope_id=scope.other",
	} {
		bad := httptest.NewRequest(http.MethodGet, raw, nil)
		bad.Host = "127.0.0.1:9000"
		bad.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
		badRR := httptest.NewRecorder()
		h.ServeHTTP(badRR, bad)
		if badRR.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", raw, badRR.Code, badRR.Body.String())
		}
	}
}

func TestWebDeviceDetailRedactsControllerNotFoundDiagnostic(t *testing.T) {
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	h.loadDeviceDetail = func(context.Context, string) (api.DeviceDetail, error) {
		return api.DeviceDetail{}, fmt.Errorf("wrapped: %w", &localapi.ResponseError{Code: "not_found", Message: "sensitive scope detail"})
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/devices/detail?device_id=device.one", nil)
	req.Host = "127.0.0.1:9000"
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), `"error":"not_found"`) || strings.Contains(rr.Body.String(), "sensitive scope detail") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
