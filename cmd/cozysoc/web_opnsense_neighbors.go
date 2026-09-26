package main

import (
	"context"
	"net/http"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func (h *webHandler) handleOPNsenseNeighbors(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "OPNsense neighbor history is read-only")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "OPNsense neighbor history does not accept parameters or a body")
		return
	}
	if h.loadOPNsenseNeighbors == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "OPNsense neighbor history is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.loadOPNsenseNeighbors(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "OPNsense neighbor history is unavailable")
		return
	}
	if result.Reports == nil {
		result.Reports = []api.OPNsenseNeighborReport{}
	}
	writeWebJSON(w, http.StatusOK, result)
}
