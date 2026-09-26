package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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
