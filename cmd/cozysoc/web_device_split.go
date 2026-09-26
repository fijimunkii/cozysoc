package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

type deviceSplitLoader func(context.Context) (api.DeviceSplitList, error)
type deviceSplitMutator func(context.Context, api.DeviceSplitParams) (api.DeviceSplitResult, error)
type deviceUnsplitMutator func(context.Context, api.DeviceUnsplitParams) (api.DeviceSplitResult, error)

func configureWebDeviceSplits(h *webHandler, stateDir string) {
	h.loadDeviceSplits = func(ctx context.Context) (api.DeviceSplitList, error) {
		result, err := localapi.NewClient(stateDir).Call(ctx, api.MethodDeviceSplits)
		if err != nil {
			return api.DeviceSplitList{}, fmt.Errorf("load controller device splits: %w", err)
		}
		var list api.DeviceSplitList
		if err := json.Unmarshal(result, &list); err != nil {
			return api.DeviceSplitList{}, fmt.Errorf("decode controller device splits: %w", err)
		}
		return list, nil
	}
	h.splitDevice = func(ctx context.Context, params api.DeviceSplitParams) (api.DeviceSplitResult, error) {
		return changeDeviceSplitWithController(ctx, stateDir, api.MethodDeviceSplit, params)
	}
	h.unsplitDevice = func(ctx context.Context, params api.DeviceUnsplitParams) (api.DeviceSplitResult, error) {
		return changeDeviceSplitWithController(ctx, stateDir, api.MethodDeviceUnsplit, params)
	}
}

func changeDeviceSplitWithController(ctx context.Context, stateDir, method string, params any) (api.DeviceSplitResult, error) {
	result, err := localapi.NewClient(stateDir).CallWithParams(ctx, method, params)
	if err != nil {
		return api.DeviceSplitResult{}, fmt.Errorf("update controller device split: %w", err)
	}
	var correction api.DeviceSplitResult
	if err := json.Unmarshal(result, &correction); err != nil {
		return api.DeviceSplitResult{}, fmt.Errorf("decode controller device split: %w", err)
	}
	return correction, nil
}

func (h *webHandler) handleDeviceSplits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "device splits are read-only on this endpoint")
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "device splits accept no query or body")
		return
	}
	if h.loadDeviceSplits == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "device splits are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	list, err := h.loadDeviceSplits(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "device splits are unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, list)
}

func (h *webHandler) handleDeviceSplit(w http.ResponseWriter, r *http.Request) {
	h.handleDeviceSplitMutation(w, r, true)
}

func (h *webHandler) handleDeviceUnsplit(w http.ResponseWriter, r *http.Request) {
	h.handleDeviceSplitMutation(w, r, false)
}

func (h *webHandler) handleDeviceSplitMutation(w http.ResponseWriter, r *http.Request, split bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "device splits do not accept query parameters")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", "device splits require JSON")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebMutationBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	var result api.DeviceSplitResult
	var err error
	if split {
		var params api.DeviceSplitParams
		if decoder.Decode(&params) != nil || ensureJSONEOF(decoder) != nil || !deviceIDPattern.MatchString(params.SourceDeviceID) || !deviceIDPattern.MatchString(params.ObservationID) || params.TargetDeviceID != "" && !deviceIDPattern.MatchString(params.TargetDeviceID) || params.TargetDeviceID == params.SourceDeviceID {
			writeWebError(w, http.StatusBadRequest, "invalid_request", "device split request is invalid")
			return
		}
		if h.splitDevice == nil {
			writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "device split is unavailable")
			return
		}
		result, err = h.splitDevice(ctx, params)
	} else {
		var params api.DeviceUnsplitParams
		if decoder.Decode(&params) != nil || ensureJSONEOF(decoder) != nil || !deviceIDPattern.MatchString(params.ObservationID) {
			writeWebError(w, http.StatusBadRequest, "invalid_request", "device split undo request is invalid")
			return
		}
		if h.unsplitDevice == nil {
			writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "device split undo is unavailable")
			return
		}
		result, err = h.unsplitDevice(ctx, params)
	}
	if err != nil {
		writeMutationControllerError(w, err, "device split could not be updated")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}
