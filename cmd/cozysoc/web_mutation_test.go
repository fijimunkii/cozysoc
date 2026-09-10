package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

const testCSRFToken = "ccccccccccccccccccccccccccccccccccccccccccc"

func newMutationTestHandler(t *testing.T) *webHandler {
	t.Helper()
	const host = "127.0.0.1:43821"
	handler := newWebHandler(host, testUIDir(t), testBootstrapToken, testSessionToken, func(context.Context) (coverageEnvelope, error) {
		return testCoverageEnvelope(), nil
	})
	handler.csrfToken = testCSRFToken
	return handler
}

func TestWebSessionInfoRequiresAuthenticationAndReturnsOnlyCSRF(t *testing.T) {
	handler := newMutationTestHandler(t)
	const target = "http://127.0.0.1:43821/api/session"

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, target, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated session info status = %d, want 401", unauthenticated.Code)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, target, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("session info status = %d body=%s", response.Code, response.Body.String())
	}
	var info webSessionInfo
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.CSRFToken != testCSRFToken {
		t.Fatalf("csrf token = %q, want test token", info.CSRFToken)
	}
	for _, forbidden := range []string{testBootstrapToken, testSessionToken} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("session info exposed forbidden credential %q", forbidden)
		}
	}
}

func TestWebNetworksReadBoundary(t *testing.T) {
	handler := newMutationTestHandler(t)
	calls := 0
	handler.loadNetworks = func(context.Context) (api.NetworkList, error) {
		calls++
		return api.NetworkList{
			Candidates: []api.NetworkInterface{{InterfaceName: "en0", InterfaceIndex: 4, Prefixes: []string{"192.0.2.0/24"}}},
		}, nil
	}
	const target = "http://127.0.0.1:43821/api/networks"

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, target, nil))
	if unauthenticated.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated networks response: status=%d calls=%d", unauthenticated.Code, calls)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, target, nil))
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body.String(), `"interface_name":"en0"`) {
		t.Fatalf("networks response: status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestWebNetworkEnrollRequiresSessionOriginAndCSRF(t *testing.T) {
	handler := newMutationTestHandler(t)
	calls := 0
	var got api.NetworkEnrollParams
	handler.enrollNetwork = func(_ context.Context, params api.NetworkEnrollParams) (api.NetworkEnrollResult, error) {
		calls++
		got = params
		return api.NetworkEnrollResult{
			ScopeID:    "scope.home",
			EnrolledAt: time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC),
			Interface:  api.NetworkInterface{InterfaceName: "en0", InterfaceIndex: 4, Prefixes: []string{"192.0.2.0/24"}},
			Changed:    true,
		}, nil
	}
	const target = "http://127.0.0.1:43821/api/networks/enroll"

	makeRequest := func(authenticated bool, origin, csrf string) *http.Request {
		var request *http.Request
		body := strings.NewReader(`{"interface_name":"en0"}`)
		if authenticated {
			request = authenticatedRequest(http.MethodPost, target, body)
		} else {
			request = httptest.NewRequest(http.MethodPost, target, body)
		}
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if csrf != "" {
			request.Header.Set(webCSRFHeader, csrf)
		}
		return request
	}

	tests := []struct {
		name          string
		authenticated bool
		origin        string
		csrf          string
		want          int
	}{
		{name: "no session", origin: "http://127.0.0.1:43821", csrf: testCSRFToken, want: http.StatusUnauthorized},
		{name: "no origin", authenticated: true, csrf: testCSRFToken, want: http.StatusForbidden},
		{name: "wrong origin", authenticated: true, origin: "https://attacker.invalid", csrf: testCSRFToken, want: http.StatusForbidden},
		{name: "no csrf", authenticated: true, origin: "http://127.0.0.1:43821", want: http.StatusForbidden},
		{name: "wrong csrf", authenticated: true, origin: "http://127.0.0.1:43821", csrf: strings.Repeat("x", len(testCSRFToken)), want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, makeRequest(tt.authenticated, tt.origin, tt.csrf))
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d, body=%s", response.Code, tt.want, response.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected enrollment reached controller %d times", calls)
	}

	valid := httptest.NewRecorder()
	handler.ServeHTTP(valid, makeRequest(true, "http://127.0.0.1:43821", testCSRFToken))
	if valid.Code != http.StatusOK || calls != 1 || got.InterfaceName != "en0" {
		t.Fatalf("valid enrollment response: status=%d calls=%d params=%+v body=%s", valid.Code, calls, got, valid.Body.String())
	}
}

func TestWebDeviceWatchMutationsAreTypedAndBodyless(t *testing.T) {
	handler := newMutationTestHandler(t)
	enableCalls := 0
	disableCalls := 0
	handler.enableDeviceWatch = func(context.Context) (api.DeviceWatchControlResult, error) {
		enableCalls++
		return api.DeviceWatchControlResult{ScopeID: "scope.home", Changed: true, Active: true, State: capability.InstanceState{Desired: capability.DesiredEnabled}}, nil
	}
	handler.disableDeviceWatch = func(context.Context) (api.DeviceWatchControlResult, error) {
		disableCalls++
		return api.DeviceWatchControlResult{ScopeID: "scope.home", Changed: true, Active: false, State: capability.InstanceState{Desired: capability.DesiredDisabled}}, nil
	}

	for _, tc := range []struct {
		path       string
		wantActive string
	}{
		{path: "/api/device-watch/enable", wantActive: `"active":true`},
		{path: "/api/device-watch/disable", wantActive: `"active":false`},
	} {
		request := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821"+tc.path, nil)
		request.Header.Set("Origin", "http://127.0.0.1:43821")
		request.Header.Set(webCSRFHeader, testCSRFToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), tc.wantActive) {
			t.Fatalf("%s response: status=%d body=%s", tc.path, response.Code, response.Body.String())
		}
	}
	if enableCalls != 1 || disableCalls != 1 {
		t.Fatalf("control calls enable=%d disable=%d", enableCalls, disableCalls)
	}

	bodyRequest := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821/api/device-watch/enable", strings.NewReader("unexpected"))
	bodyRequest.Header.Set("Origin", "http://127.0.0.1:43821")
	bodyRequest.Header.Set(webCSRFHeader, testCSRFToken)
	bodyResponse := httptest.NewRecorder()
	handler.ServeHTTP(bodyResponse, bodyRequest)
	if bodyResponse.Code != http.StatusBadRequest || enableCalls != 1 {
		t.Fatalf("bodyful control status=%d enableCalls=%d", bodyResponse.Code, enableCalls)
	}
}

func TestWebMutationMapsTypedControllerErrors(t *testing.T) {
	for _, tc := range []struct {
		code    string
		want    int
		webCode string
	}{
		{code: "invalid_request", want: http.StatusBadRequest, webCode: "invalid_request"},
		{code: "precondition_failed", want: http.StatusPreconditionFailed, webCode: "precondition_failed"},
		{code: "conflict", want: http.StatusConflict, webCode: "conflict"},
		{code: "not_found", want: http.StatusNotFound, webCode: "not_found"},
		{code: "method_not_found", want: http.StatusNotImplemented, webCode: "unsupported_operation"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			handler := newMutationTestHandler(t)
			handler.enableDeviceWatch = func(context.Context) (api.DeviceWatchControlResult, error) {
				return api.DeviceWatchControlResult{}, fmtControllerError(tc.code)
			}
			request := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821/api/device-watch/enable", nil)
			request.Header.Set("Origin", "http://127.0.0.1:43821")
			request.Header.Set(webCSRFHeader, testCSRFToken)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want || !strings.Contains(response.Body.String(), `"error":"`+tc.webCode+`"`) {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}

	handler := newMutationTestHandler(t)
	handler.enableDeviceWatch = func(context.Context) (api.DeviceWatchControlResult, error) {
		return api.DeviceWatchControlResult{}, errors.New("transport offline")
	}
	request := authenticatedRequest(http.MethodPost, "http://127.0.0.1:43821/api/device-watch/enable", nil)
	request.Header.Set("Origin", "http://127.0.0.1:43821")
	request.Header.Set(webCSRFHeader, testCSRFToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"error":"controller_unavailable"`) {
		t.Fatalf("transport error response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func fmtControllerError(code string) error {
	return &localapi.ResponseError{Code: code, Message: "test controller message"}
}
