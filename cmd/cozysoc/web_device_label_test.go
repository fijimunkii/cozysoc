package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func TestWebDeviceLabelRequiresCSRFBeforeMutator(t *testing.T) {
	h := labelTestHandler(t)
	called := false
	h.labelDevice = func(context.Context, api.DeviceLabelParams) (api.DeviceLabelResult, error) {
		called = true
		return api.DeviceLabelResult{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/devices/label", strings.NewReader(`{"device_id":"device.one","label":"Kitchen"}`))
	req.Host = "127.0.0.1:9000"
	req.Header.Set("Origin", "http://127.0.0.1:9000")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("status=%d called=%v body=%s", rr.Code, called, rr.Body.String())
	}
}

func TestWebDeviceLabelAcceptsEmptyLabelForClear(t *testing.T) {
	h := labelTestHandler(t)
	var got api.DeviceLabelParams
	h.labelDevice = func(_ context.Context, params api.DeviceLabelParams) (api.DeviceLabelResult, error) {
		got = params
		return api.DeviceLabelResult{DeviceID: params.DeviceID, Changed: true}, nil
	}
	rr := performLabelRequest(t, h, `{"device_id":"device.one","label":""}`)
	if rr.Code != http.StatusOK || got.DeviceID != "device.one" || got.Label == nil || *got.Label != "" {
		t.Fatalf("status=%d params=%+v body=%s", rr.Code, got, rr.Body.String())
	}
}

func TestWebDeviceLabelRejectsMalformedBodyBeforeMutator(t *testing.T) {
	h := labelTestHandler(t)
	called := false
	h.labelDevice = func(context.Context, api.DeviceLabelParams) (api.DeviceLabelResult, error) {
		called = true
		return api.DeviceLabelResult{}, nil
	}
	for _, body := range []string{
		`{"device_id":"device.one"}`,
		`{"device_id":"device.one","label":"Kitchen","extra":true}`,
		`{"device_id":"device.one","label":"Kitchen"} {}`,
	} {
		rr := performLabelRequest(t, h, body)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rr.Code, rr.Body.String())
		}
	}
	if called {
		t.Fatal("malformed device label request reached controller mutator")
	}
}

func TestWebDeviceLabelMapsScopedNotFoundWithoutRawDiagnostic(t *testing.T) {
	h := labelTestHandler(t)
	h.labelDevice = func(context.Context, api.DeviceLabelParams) (api.DeviceLabelResult, error) {
		return api.DeviceLabelResult{}, fmt.Errorf("wrapped: %w", &localapi.ResponseError{Code: "not_found", Message: "sensitive internal target detail"})
	}
	rr := performLabelRequest(t, h, `{"device_id":"device.one","label":"Kitchen"}`)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), `"error":"not_found"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sensitive internal target detail") {
		t.Fatalf("raw controller diagnostic leaked: %s", rr.Body.String())
	}
}

func labelTestHandler(t *testing.T) *webHandler {
	t.Helper()
	h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
	h.csrfToken = "csrf"
	return h
}

func performLabelRequest(t *testing.T, h *webHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/devices/label", strings.NewReader(body))
	req.Host = "127.0.0.1:9000"
	req.Header.Set("Origin", "http://127.0.0.1:9000")
	req.Header.Set(webCSRFHeader, "csrf")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
