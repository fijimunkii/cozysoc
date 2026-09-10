package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

const (
	webCSRFHeader           = "X-Cozy-CSRF"
	maxWebMutationBodyBytes = 1024
)

type networkLoader func(context.Context) (api.NetworkList, error)
type networkEnrollMutator func(context.Context, api.NetworkEnrollParams) (api.NetworkEnrollResult, error)
type deviceWatchMutator func(context.Context) (api.DeviceWatchControlResult, error)

type webSessionInfo struct {
	CSRFToken string `json:"csrf_token"`
}

func configureWebMutationBridge(handler *webHandler, stateDir string) error {
	csrfToken, err := newWebToken()
	if err != nil {
		return fmt.Errorf("generate local web CSRF token: %w", err)
	}
	handler.csrfToken = csrfToken
	handler.loadNetworks = func(ctx context.Context) (api.NetworkList, error) {
		return loadNetworksFromController(ctx, stateDir)
	}
	handler.enrollNetwork = func(ctx context.Context, params api.NetworkEnrollParams) (api.NetworkEnrollResult, error) {
		return enrollNetworkWithController(ctx, stateDir, params)
	}
	handler.enableDeviceWatch = func(ctx context.Context) (api.DeviceWatchControlResult, error) {
		return controlDeviceWatchWithController(ctx, stateDir, api.MethodDeviceWatchEnable)
	}
	handler.disableDeviceWatch = func(ctx context.Context) (api.DeviceWatchControlResult, error) {
		return controlDeviceWatchWithController(ctx, stateDir, api.MethodDeviceWatchDisable)
	}
	return nil
}

func loadNetworksFromController(ctx context.Context, stateDir string) (api.NetworkList, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodNetworksList)
	if err != nil {
		return api.NetworkList{}, fmt.Errorf("load controller networks: %w", err)
	}
	var networks api.NetworkList
	if err := json.Unmarshal(result, &networks); err != nil {
		return api.NetworkList{}, fmt.Errorf("decode controller networks: %w", err)
	}
	return networks, nil
}

func enrollNetworkWithController(ctx context.Context, stateDir string, params api.NetworkEnrollParams) (api.NetworkEnrollResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).CallWithParams(requestCtx, api.MethodNetworkEnroll, params)
	if err != nil {
		return api.NetworkEnrollResult{}, fmt.Errorf("enroll controller network: %w", err)
	}
	var enrolled api.NetworkEnrollResult
	if err := json.Unmarshal(result, &enrolled); err != nil {
		return api.NetworkEnrollResult{}, fmt.Errorf("decode controller network enrollment: %w", err)
	}
	return enrolled, nil
}

func controlDeviceWatchWithController(ctx context.Context, stateDir, method string) (api.DeviceWatchControlResult, error) {
	if method != api.MethodDeviceWatchEnable && method != api.MethodDeviceWatchDisable {
		return api.DeviceWatchControlResult{}, fmt.Errorf("unsupported Device Watch browser control method")
	}
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, method)
	if err != nil {
		return api.DeviceWatchControlResult{}, fmt.Errorf("update controller Device Watch state: %w", err)
	}
	var control api.DeviceWatchControlResult
	if err := json.Unmarshal(result, &control); err != nil {
		return api.DeviceWatchControlResult{}, fmt.Errorf("decode controller Device Watch control: %w", err)
	}
	return control, nil
}

func (h *webHandler) handleSessionRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleSessionInfo(w, r)
	case http.MethodPost:
		h.handleSession(w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "web session supports GET and POST")
	}
}

func (h *webHandler) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "web session info does not accept query parameters")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "web session info does not accept a body")
		return
	}
	if h.csrfToken == "" {
		writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "browser mutations are unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, webSessionInfo{CSRFToken: h.csrfToken})
}

func (h *webHandler) handleNetworks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "networks are read-only on this endpoint")
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "network requests do not accept query parameters")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "network requests do not accept a body")
		return
	}
	if h.loadNetworks == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live network state is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	networks, err := h.loadNetworks(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live network state is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, networks)
}

func (h *webHandler) handleNetworkEnroll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "network enrollment does not accept query parameters")
		return
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", "network enrollment requires JSON")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebMutationBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var params api.NetworkEnrollParams
	if err := decoder.Decode(&params); err != nil || params.InterfaceName == "" {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "network enrollment request is invalid")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "network enrollment request is invalid")
		return
	}
	if h.enrollNetwork == nil {
		writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "network enrollment is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.enrollNetwork(ctx, params)
	if err != nil {
		writeMutationControllerError(w, err, "network enrollment could not be completed")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}

func (h *webHandler) handleDeviceWatchEnable(w http.ResponseWriter, r *http.Request) {
	h.handleDeviceWatchMutation(w, r, h.enableDeviceWatch)
}

func (h *webHandler) handleDeviceWatchDisable(w http.ResponseWriter, r *http.Request) {
	h.handleDeviceWatchMutation(w, r, h.disableDeviceWatch)
}

func (h *webHandler) handleDeviceWatchMutation(w http.ResponseWriter, r *http.Request, mutate deviceWatchMutator) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "Device Watch control does not accept query parameters")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "Device Watch control does not accept a body")
		return
	}
	if mutate == nil {
		writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "Device Watch control is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := mutate(ctx)
	if err != nil {
		writeMutationControllerError(w, err, "Device Watch state could not be updated")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}

func (h *webHandler) authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "state changes require POST")
		return false
	}
	if r.Header.Get("Origin") != "http://"+h.expectedHost {
		writeWebError(w, http.StatusForbidden, "origin_required", "state changes require the exact local origin")
		return false
	}
	candidate := r.Header.Get(webCSRFHeader)
	if h.csrfToken == "" || len(candidate) != len(h.csrfToken) || subtle.ConstantTimeCompare([]byte(candidate), []byte(h.csrfToken)) != 1 {
		writeWebError(w, http.StatusForbidden, "csrf_required", "state changes require the current CSRF token")
		return false
	}
	return true
}

func writeMutationControllerError(w http.ResponseWriter, err error, fallback string) {
	var responseErr *localapi.ResponseError
	if errors.As(err, &responseErr) {
		switch responseErr.Code {
		case "invalid_request":
			writeWebError(w, http.StatusBadRequest, "invalid_request", "the requested change is invalid")
			return
		case "not_found":
			writeWebError(w, http.StatusNotFound, "not_found", "the requested target is not available")
			return
		case "precondition_failed":
			writeWebError(w, http.StatusPreconditionFailed, "precondition_failed", "required prerequisites are not satisfied")
			return
		case "conflict":
			writeWebError(w, http.StatusConflict, "conflict", "the requested change conflicts with current state")
			return
		case "method_not_found":
			writeWebError(w, http.StatusNotImplemented, "unsupported_operation", "this controller does not support the requested operation")
			return
		}
	}
	writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", fallback)
}
