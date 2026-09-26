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

func safeDiagnosticFixture() api.DiagnosticPreview {
	return api.DiagnosticPreview{
		SchemaVersion: 2, GeneratedAt: time.Unix(1_800_000_000, 0).UTC(),
		Controller: api.DiagnosticController{BuildVersion: "dev", ConfigSchemaVersion: 4, HealthState: "ok"},
		Modules:    []api.DiagnosticModule{{ID: "device-watch", BuildVersion: "dev", Desired: "enabled", Verification: "degraded"}},
		Coverage:   []api.DiagnosticCoverage{{CapabilityID: "device-watch", State: "degraded", FailureCategory: "sensor"}},
		Storage:    api.DiagnosticStorage{ReadState: "current", QuotaState: "pressure", VolumeState: "current"},
	}
}

func TestWebDiagnosticPreviewRejectsUnreviewedInputs(t *testing.T) {
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	calls := 0
	handler.loadDiagnostics = func(context.Context) (api.DiagnosticPreview, error) { calls++; return safeDiagnosticFixture(), nil }
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "http://"+host+"/api/diagnostics/preview", nil),
		authenticatedRequest(http.MethodPost, "http://"+host+"/api/diagnostics/preview", nil),
		authenticatedRequest(http.MethodGet, "http://"+host+"/api/diagnostics/preview?scope=private", nil),
		authenticatedRequest(http.MethodGet, "http://"+host+"/api/diagnostics/preview", strings.NewReader("body")),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code < 400 {
			t.Fatalf("unreviewed read succeeded: %d", response.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("rejected requests reached controller %d times", calls)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/diagnostics/preview", nil))
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body.String(), `"failure_category":"sensor"`) {
		t.Fatalf("preview = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("diagnostic preview was cacheable")
	}
}

func TestWebDiagnosticPreviewRejectsMaliciousNativeFieldWithoutEcho(t *testing.T) {
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	handler.loadDiagnostics = func(context.Context) (api.DiagnosticPreview, error) {
		preview := safeDiagnosticFixture()
		preview.Coverage[0].FailureCategory = "https://private.invalid/?token=abc"
		return preview, nil
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/diagnostics/preview", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private.invalid") || strings.Contains(response.Body.String(), "token=abc") {
		t.Fatalf("unsafe preview = %d %s", response.Code, response.Body.String())
	}
}

func TestWebDiagnosticPreviewRejectsUnreviewedStorageFieldWithoutEcho(t *testing.T) {
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	handler.loadDiagnostics = func(context.Context) (api.DiagnosticPreview, error) {
		preview := safeDiagnosticFixture()
		preview.Storage.QuotaState = "private /Users/name/token=abc"
		return preview, nil
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/diagnostics/preview", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "token=abc") {
		t.Fatalf("unsafe storage preview = %d %s", response.Code, response.Body.String())
	}
}
