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

func loadArrivalFindingsFromController(ctx context.Context, stateDir string) (api.ArrivalFindingList, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	raw, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodArrivalFindings)
	if err != nil {
		return api.ArrivalFindingList{}, fmt.Errorf("load arrival findings: %w", err)
	}
	var result api.ArrivalFindingList
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.ArrivalFindingList{}, fmt.Errorf("decode arrival findings: %w", err)
	}
	return result, nil
}

func (h *webHandler) handleArrivalFindings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "arrival findings") {
		return
	}
	if h.loadArrivalFindings == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "arrival findings are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.loadArrivalFindings(ctx)
	if err != nil || len(result.Items) > 100 {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "arrival findings are unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}

func acknowledgeArrivalFindingWithController(ctx context.Context, stateDir string, params api.ArrivalAcknowledgeParams) (api.ArrivalAcknowledgeResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	raw, err := localapi.NewClient(stateDir).CallWithParams(requestCtx, api.MethodArrivalAcknowledge, params)
	if err != nil {
		return api.ArrivalAcknowledgeResult{}, fmt.Errorf("acknowledge arrival finding: %w", err)
	}
	var result api.ArrivalAcknowledgeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.ArrivalAcknowledgeResult{}, fmt.Errorf("decode arrival acknowledgement: %w", err)
	}
	return result, nil
}

func (h *webHandler) handleArrivalAcknowledge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", "arrival acknowledgement does not accept query parameters")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", "arrival acknowledgement requires JSON")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebMutationBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var params api.ArrivalAcknowledgeParams
	if err := decoder.Decode(&params); err != nil || !deviceIDPattern.MatchString(params.FindingID) {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "arrival acknowledgement request is invalid")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "arrival acknowledgement request is invalid")
		return
	}
	if h.acknowledgeArrival == nil {
		writeWebError(w, http.StatusServiceUnavailable, "mutation_unavailable", "arrival acknowledgement is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.acknowledgeArrival(ctx, params)
	if err != nil {
		writeMutationControllerError(w, err, "arrival acknowledgement could not be saved")
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}
