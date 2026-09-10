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

type deviceLabelMutator func(context.Context, api.DeviceLabelParams) (api.DeviceLabelResult, error)

func configureWebDeviceLabel(handler *webHandler, stateDir string) {
	handler.labelDevice = func(ctx context.Context, params api.DeviceLabelParams) (api.DeviceLabelResult, error) {
		return labelDeviceWithController(ctx, stateDir, params)
	}
}

func labelDeviceWithController(ctx context.Context, stateDir string, params api.DeviceLabelParams) (api.DeviceLabelResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).CallWithParams(requestCtx, api.MethodDeviceLabel, params)
	if err != nil {
		return api.DeviceLabelResult{}, fmt.Errorf("update controller device label: %w", err)
	}
	var updated api.DeviceLabelResult
	if err := json.Unmarshal(result, &updated); err != nil {
		return api.DeviceLabelResult{}, fmt.Errorf("decode controller device label response: %w", err)
	}
	return updated, nil
}

func (h *webHandler) handleDeviceLabel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "device label changes do not accept query parameters")
		return
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", "device label changes require JSON")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebMutationBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var params api.DeviceLabelParams
	if err := decoder.Decode(&params); err != nil || params.DeviceID == "" || params.Label == nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "device label request is invalid")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "device label request is invalid")
		return
	}
	if h.labelDevice == nil {
		writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "device labeling is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.labelDevice(ctx, params)
	if err != nil {
		writeMutationControllerError(w, err, "device label could not be updated")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}
