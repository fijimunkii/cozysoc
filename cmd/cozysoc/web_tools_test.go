package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

func TestWebStatusIsAuthenticatedReadOnlyAndMinimized(t *testing.T) {
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	handler.loadStatus = func(context.Context) (api.Status, error) {
		calls++
		return api.Status{APIVersion: 1, ControllerVersion: "dev", PID: 4242, StartedAt: time.Unix(1_800_000_000, 0).UTC(), UptimeMS: 999, ConfigSchemaVersion: 1, Transport: "unix"}, nil
	}

	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/status", nil))
	if unauth.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated status=%d calls=%d body=%s", unauth.Code, calls, unauth.Body.String())
	}
	for _, test := range []struct {
		method, url, body string
		want              int
	}{
		{http.MethodPost, "http://" + host + "/api/status", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "http://" + host + "/api/status?pid=1", "", http.StatusBadRequest},
		{http.MethodGet, "http://" + host + "/api/status", "unexpected", http.StatusBadRequest},
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
		t.Fatalf("rejected status requests reached loader %d times", calls)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/status", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(body, `"controller_version":"dev"`) {
		t.Fatalf("status response status=%d calls=%d body=%s", response.Code, calls, body)
	}
	for _, forbidden := range []string{`"pid"`, `"uptime_ms"`, `"api_version"`, testBootstrapToken, testSessionToken} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("status response exposed forbidden detail %q: %s", forbidden, body)
		}
	}
}

func TestWebCapabilitiesProjectsOnlyToolPresentationFields(t *testing.T) {
	const host = "127.0.0.1:43821"
	calls := 0
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) { return testCoverageEnvelope(), nil })
	handler.loadCapabilities = func(context.Context) (api.CapabilityList, error) {
		calls++
		return api.CapabilityList{CatalogSchemaVersion: 1, Capabilities: []capability.Instance{{
			Manifest: capability.Manifest{
				SchemaVersion: 1, ID: "device-watch", DisplayName: "Device Watch", Summary: "Visible devices only.", Release: "v0.1",
				Ownership:    []capability.OwnershipMode{capability.OwnershipBuiltin},
				Targets:      []capability.Target{{OS: "darwin", Arch: "arm64", MinVersion: "13.0", Support: capability.SupportCandidate, Evidence: "private/path.md"}},
				Inputs:       []capability.InputRequirement{{ID: "hidden-input", Required: true, Description: "not browser presentation"}},
				Privileges:   []capability.PrivilegeRequirement{{ID: "local-network-access", Requirement: capability.RequirementConditional, Description: "Local permission may be required."}},
				Dependencies: []capability.Dependency{{ID: "hidden-runtime", Kind: capability.DependencyRuntime, Required: true}},
				Config:       capability.ConfigContract{SchemaVersion: 1, Fields: []capability.ConfigField{{Name: "secret_ref", Type: capability.ConfigSecretRef}}},
				Resources:    capability.ResourceBudget{Measurement: capability.MeasurementUnmeasured, Profile: "desktop-base", Evidence: "Measurements remain release-gated."},
				Provenance:   capability.Provenance{Kind: "first-party", License: "MIT", Source: "https://secret.invalid/source", VersionPolicy: "Ships with the controller."},
				Health:       capability.HealthContract{ProcessRequired: false, VerificationSignals: []string{"observation-freshness"}, CoverageRequiresVerification: true},
				DeepLinks:    []capability.DeepLink{{ID: "declared-link", AllowedContexts: []string{"device"}}},
				Lifecycle:    []capability.LifecycleAction{capability.ActionPreflight, capability.ActionEnable, capability.ActionVerify, capability.ActionDisable},
			},
			Configured: true, Ownership: capability.OwnershipBuiltin,
			State: capability.InstanceState{Desired: capability.DesiredEnabled, Process: capability.ProcessNotApplicable, Verification: capability.VerificationVerified},
		}}}, nil
	}

	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/capabilities", nil))
	if unauth.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated capabilities status=%d calls=%d body=%s", unauth.Code, calls, unauth.Body.String())
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "http://"+host+"/api/capabilities", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(body, `"display_name":"Device Watch"`) || !strings.Contains(body, `"deep_link_count":1`) {
		t.Fatalf("capabilities response status=%d calls=%d body=%s", response.Code, calls, body)
	}
	for _, forbidden := range []string{`"config"`, `"dependencies"`, `"inputs"`, "hidden-runtime", "hidden-input", "secret_ref", "private/path.md", "https://secret.invalid/source", testBootstrapToken, testSessionToken} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("capabilities response exposed native-only detail %q: %s", forbidden, body)
		}
	}
}
