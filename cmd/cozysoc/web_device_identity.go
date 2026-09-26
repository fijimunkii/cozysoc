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

type deviceMergeLoader func(context.Context) (api.DeviceMergeList, error)
type deviceMergeMutator func(context.Context, api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error)
type deviceUnmergeMutator func(context.Context, api.DeviceUnmergeParams) (api.DeviceIdentityCorrectionResult, error)

func configureWebDeviceIdentity(h *webHandler, stateDir string) {
	h.loadDeviceMerges = func(ctx context.Context) (api.DeviceMergeList, error) {
		result, err := localapi.NewClient(stateDir).Call(ctx, api.MethodDeviceMerges)
		if err != nil {
			return api.DeviceMergeList{}, fmt.Errorf("load controller device corrections: %w", err)
		}
		var list api.DeviceMergeList
		if err := json.Unmarshal(result, &list); err != nil {
			return api.DeviceMergeList{}, fmt.Errorf("decode controller device corrections: %w", err)
		}
		return list, nil
	}
	h.mergeDevice = func(ctx context.Context, params api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error) {
		return changeDeviceIdentityWithController(ctx, stateDir, api.MethodDeviceMerge, params)
	}
	h.unmergeDevice = func(ctx context.Context, params api.DeviceUnmergeParams) (api.DeviceIdentityCorrectionResult, error) {
		return changeDeviceIdentityWithController(ctx, stateDir, api.MethodDeviceUnmerge, params)
	}
}

func changeDeviceIdentityWithController(ctx context.Context, stateDir, method string, params any) (api.DeviceIdentityCorrectionResult, error) {
	result, err := localapi.NewClient(stateDir).CallWithParams(ctx, method, params)
	if err != nil {
		return api.DeviceIdentityCorrectionResult{}, fmt.Errorf("update controller device identity: %w", err)
	}
	var correction api.DeviceIdentityCorrectionResult
	if err := json.Unmarshal(result, &correction); err != nil {
		return api.DeviceIdentityCorrectionResult{}, fmt.Errorf("decode controller device identity correction: %w", err)
	}
	return correction, nil
}

func (h *webHandler) handleDeviceMerges(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "device corrections are read-only on this endpoint")
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "device corrections accept no query or body")
		return
	}
	if h.loadDeviceMerges == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "device corrections are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	list, err := h.loadDeviceMerges(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "device corrections are unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, list)
}

func (h *webHandler) handleDeviceMerge(w http.ResponseWriter, r *http.Request) {
	h.handleDeviceIdentityMutation(w, r, true)
}

func (h *webHandler) handleDeviceUnmerge(w http.ResponseWriter, r *http.Request) {
	h.handleDeviceIdentityMutation(w, r, false)
}

func (h *webHandler) handleDeviceIdentityMutation(w http.ResponseWriter, r *http.Request, merge bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "device identity changes do not accept query parameters")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", "device identity changes require JSON")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebMutationBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	var result api.DeviceIdentityCorrectionResult
	var err error
	if merge {
		var params api.DeviceMergeParams
		if decoder.Decode(&params) != nil || ensureJSONEOF(decoder) != nil || !deviceIDPattern.MatchString(params.SourceDeviceID) || !deviceIDPattern.MatchString(params.TargetDeviceID) || params.SourceDeviceID == params.TargetDeviceID {
			writeWebError(w, http.StatusBadRequest, "invalid_request", "device merge request is invalid")
			return
		}
		if h.mergeDevice == nil {
			writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "device merge is unavailable")
			return
		}
		result, err = h.mergeDevice(ctx, params)
	} else {
		var params api.DeviceUnmergeParams
		if decoder.Decode(&params) != nil || ensureJSONEOF(decoder) != nil || !deviceIDPattern.MatchString(params.SourceDeviceID) {
			writeWebError(w, http.StatusBadRequest, "invalid_request", "device undo request is invalid")
			return
		}
		if h.unmergeDevice == nil {
			writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "device undo is unavailable")
			return
		}
		result, err = h.unmergeDevice(ctx, params)
	}
	if err != nil {
		writeMutationControllerError(w, err, "device identity could not be updated")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}
