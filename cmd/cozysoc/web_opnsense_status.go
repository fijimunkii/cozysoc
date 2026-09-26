package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

// Only the validated, credential-free origin and exact supported version cross
// into the browser. Router names, API credentials and neighbor data do not.
func projectWebOPNsenseStatus(status api.OPNsenseConnection) (api.OPNsenseConnection, bool) {
	if !status.Connected {
		return api.OPNsenseConnection{Connected: false}, true
	}
	if status.Version != opnsense.SupportedVersion {
		return api.OPNsenseConnection{}, false
	}
	if _, err := opnsense.NewClient(status.Endpoint, "preflight", secretstore.NewSecret([]byte("preflight")), nil); err != nil {
		return api.OPNsenseConnection{}, false
	}
	parsed, err := url.Parse(status.Endpoint)
	if err != nil {
		return api.OPNsenseConnection{}, false
	}
	return api.OPNsenseConnection{Connected: true, Endpoint: parsed.Scheme + "://" + parsed.Host, Version: status.Version}, true
}

func (h *webHandler) handleOPNsenseStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "OPNsense status is read-only")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "OPNsense status does not accept parameters or a body")
		return
	}
	if h.loadOPNsenseStatus == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "OPNsense status is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	status, err := h.loadOPNsenseStatus(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "OPNsense status is unavailable")
		return
	}
	projected, ok := projectWebOPNsenseStatus(status)
	if !ok {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "OPNsense status is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, projected)
}
