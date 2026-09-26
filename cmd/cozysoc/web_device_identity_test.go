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

func TestWebDeviceIdentityRequiresSessionAndCSRF(t *testing.T) {
	h := labelTestHandler(t)
	called := false
	h.mergeDevice = func(context.Context, api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error) {
		called = true
		return api.DeviceIdentityCorrectionResult{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/devices/merge", strings.NewReader(`{"source_device_id":"device.a","target_device_id":"device.b"}`))
	req.Header.Set("Origin", "http://127.0.0.1:9000")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || called {
		t.Fatal("missing CSRF reached mutator", rr.Code, called)
	}
}

func TestWebDeviceIdentityValidatesAndRoutesChanges(t *testing.T) {
	h := labelTestHandler(t)
	var merged api.DeviceMergeParams
	var undone api.DeviceUnmergeParams
	h.mergeDevice = func(_ context.Context, p api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error) {
		merged = p
		return api.DeviceIdentityCorrectionResult{SourceDeviceID: p.SourceDeviceID, TargetDeviceID: p.TargetDeviceID, Changed: true}, nil
	}
	h.unmergeDevice = func(_ context.Context, p api.DeviceUnmergeParams) (api.DeviceIdentityCorrectionResult, error) {
		undone = p
		return api.DeviceIdentityCorrectionResult{SourceDeviceID: p.SourceDeviceID, Changed: true}, nil
	}
	for _, body := range []string{
		`{"source_device_id":"device.a"}`,
		`{"source_device_id":"device.a","target_device_id":"device.a"}`,
		`{"source_device_id":"device.a","target_device_id":"device.b","scope_id":"scope.other"}`,
		`{"source_device_id":"device.a","target_device_id":"device.b"} {}`,
	} {
		if rr := deviceIdentityRequest(t, h, "/api/devices/merge", body); rr.Code != http.StatusBadRequest {
			t.Fatal("bad merge accepted", body, rr.Code)
		}
	}
	if merged.SourceDeviceID != "" {
		t.Fatal("bad merge reached controller")
	}
	if rr := deviceIdentityRequest(t, h, "/api/devices/merge", `{"source_device_id":"device.a","target_device_id":"device.b"}`); rr.Code != http.StatusOK || merged.SourceDeviceID != "device.a" || merged.TargetDeviceID != "device.b" {
		t.Fatal("merge failed", rr.Code, merged, rr.Body.String())
	}
	if rr := deviceIdentityRequest(t, h, "/api/devices/unmerge", `{"source_device_id":"device.a"}`); rr.Code != http.StatusOK || undone.SourceDeviceID != "device.a" {
		t.Fatal("undo failed", rr.Code, undone, rr.Body.String())
	}
	if rr := deviceIdentityRequest(t, h, "/api/devices/unmerge", `{"source_device_id":"device.a","target_device_id":"device.b"}`); rr.Code != http.StatusBadRequest {
		t.Fatal("undo accepted extra target", rr.Code)
	}
}

func TestWebDeviceIdentityListAndSafeErrors(t *testing.T) {
	h := labelTestHandler(t)
	h.loadDeviceMerges = func(context.Context) (api.DeviceMergeList, error) {
		return api.DeviceMergeList{Configured: true, ScopeID: "scope.home", Merges: []api.DeviceMerge{}}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/devices/merges", nil)
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"scope_id":"scope.home"`) {
		t.Fatal("list failed", rr.Code, rr.Body.String())
	}
	h.mergeDevice = func(context.Context, api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error) {
		return api.DeviceIdentityCorrectionResult{}, fmt.Errorf("wrapped: %w", &localapi.ResponseError{Code: "conflict", Message: "sensitive mapping"})
	}
	rr = deviceIdentityRequest(t, h, "/api/devices/merge", `{"source_device_id":"device.a","target_device_id":"device.b"}`)
	if rr.Code != http.StatusConflict || strings.Contains(rr.Body.String(), "sensitive mapping") {
		t.Fatal("unsafe conflict", rr.Code, rr.Body.String())
	}
}

func deviceIdentityRequest(t *testing.T, h *webHandler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000"+path, strings.NewReader(body))
	req.Header.Set("Origin", "http://127.0.0.1:9000")
	req.Header.Set(webCSRFHeader, "csrf")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
