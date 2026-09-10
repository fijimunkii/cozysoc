package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func loadDeviceActivityFromController(ctx context.Context, stateDir string) (api.DeviceActivityList, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodDeviceActivity)
	if err != nil {
		return api.DeviceActivityList{}, fmt.Errorf("load controller device activity: %w", err)
	}
	var activity api.DeviceActivityList
	if err := json.Unmarshal(result, &activity); err != nil {
		return api.DeviceActivityList{}, fmt.Errorf("decode controller device activity: %w", err)
	}
	return activity, nil
}

func (h *webHandler) handleDeviceActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "device activity is read-only")
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "device activity does not accept query parameters")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "device activity does not accept a body")
		return
	}
	if h.loadDeviceActivity == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live device activity is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	activity, err := h.loadDeviceActivity(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live device activity is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, activity)
}
