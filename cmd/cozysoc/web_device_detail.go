package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func loadDeviceDetailFromController(ctx context.Context, stateDir, deviceID string) (api.DeviceDetail, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).CallWithParams(requestCtx, api.MethodDeviceDetail, api.DeviceDetailParams{DeviceID: deviceID})
	if err != nil {
		return api.DeviceDetail{}, fmt.Errorf("load controller device detail: %w", err)
	}
	var detail api.DeviceDetail
	if err := json.Unmarshal(result, &detail); err != nil {
		return api.DeviceDetail{}, fmt.Errorf("decode controller device detail: %w", err)
	}
	return detail, nil
}

func (h *webHandler) handleDeviceDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "device detail is read-only")
		return
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", "device detail does not accept a body")
		return
	}
	query := r.URL.Query()
	deviceIDs, ok := query["device_id"]
	if !ok || len(query) != 1 || len(deviceIDs) != 1 || !deviceIDPattern.MatchString(deviceIDs[0]) {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "device detail requires one valid device_id")
		return
	}
	if h.loadDeviceDetail == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live device evidence is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	detail, err := h.loadDeviceDetail(ctx, deviceIDs[0])
	if err != nil {
		var responseErr *localapi.ResponseError
		if errors.As(err, &responseErr) {
			switch responseErr.Code {
			case "not_found":
				writeWebError(w, http.StatusNotFound, "not_found", "device evidence is not available in the current authorized scope")
				return
			case "invalid_request":
				writeWebError(w, http.StatusBadRequest, "invalid_request", "device detail request is invalid")
				return
			}
		}
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "live device evidence is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, detail)
}
