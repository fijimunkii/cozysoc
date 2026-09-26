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

func TestWebDeviceSplitRequiresSessionCSRFAndValidParams(t *testing.T) {
	h := labelTestHandler(t)
	called := false
	h.splitDevice = func(_ context.Context, p api.DeviceSplitParams) (api.DeviceSplitResult, error) {
		called = true
		return api.DeviceSplitResult{ObservationID: p.ObservationID, SourceDeviceID: p.SourceDeviceID, TargetDeviceID: "device.created", Changed: true}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/devices/split", strings.NewReader(`{"source_device_id":"device.a","observation_id":"obs.one"}`))
	req.Header.Set("Origin", "http://127.0.0.1:9000")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || called {
		t.Fatal("missing CSRF reached split", rr.Code, called)
	}
	for _, body := range []string{
		`{"source_device_id":"device.a"}`,
		`{"source_device_id":"device.a","observation_id":"../bad"}`,
		`{"source_device_id":"device.a","observation_id":"obs.one","scope_id":"scope.other"}`,
		`{"source_device_id":"device.a","observation_id":"obs.one","target_device_id":"device.a"}`,
		`{"source_device_id":"device.a","observation_id":"obs.one"} {}`,
	} {
		if rr := deviceIdentityRequest(t, h, "/api/devices/split", body); rr.Code != http.StatusBadRequest {
			t.Fatal("bad split accepted", body, rr.Code)
		}
	}
	if called {
		t.Fatal("bad split reached controller")
	}
	if rr := deviceIdentityRequest(t, h, "/api/devices/split", `{"source_device_id":"device.a","observation_id":"obs.one"}`); rr.Code != http.StatusOK || !called {
		t.Fatal("split failed", rr.Code, rr.Body.String())
	}
}

func TestWebDeviceSplitListUndoAndSafeErrors(t *testing.T) {
	h := labelTestHandler(t)
	h.loadDeviceSplits = func(context.Context) (api.DeviceSplitList, error) {
		return api.DeviceSplitList{Configured: true, ScopeID: "scope.home", Splits: []api.DeviceSplit{}}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9000/api/devices/splits", nil)
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"scope_id":"scope.home"`) {
		t.Fatal("split list failed", rr.Code, rr.Body.String())
	}
	var undone api.DeviceUnsplitParams
	h.unsplitDevice = func(_ context.Context, p api.DeviceUnsplitParams) (api.DeviceSplitResult, error) {
		undone = p
		return api.DeviceSplitResult{ObservationID: p.ObservationID, Changed: true}, nil
	}
	if rr := deviceIdentityRequest(t, h, "/api/devices/unsplit", `{"observation_id":"obs.one"}`); rr.Code != http.StatusOK || undone.ObservationID != "obs.one" {
		t.Fatal("unsplit failed", rr.Code, undone)
	}
	if rr := deviceIdentityRequest(t, h, "/api/devices/unsplit", `{"observation_id":"obs.one","scope_id":"scope.other"}`); rr.Code != http.StatusBadRequest {
		t.Fatal("scope override accepted", rr.Code)
	}
	h.splitDevice = func(context.Context, api.DeviceSplitParams) (api.DeviceSplitResult, error) {
		return api.DeviceSplitResult{}, fmt.Errorf("wrapped: %w", &localapi.ResponseError{Code: "conflict", Message: "private split data"})
	}
	rr = deviceIdentityRequest(t, h, "/api/devices/split", `{"source_device_id":"device.a","observation_id":"obs.one"}`)
	if rr.Code != http.StatusConflict || strings.Contains(rr.Body.String(), "private split data") {
		t.Fatal("unsafe split error", rr.Code, rr.Body.String())
	}
}
