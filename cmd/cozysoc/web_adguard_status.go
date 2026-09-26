package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

// Browser status excludes the configured username and every credential field.
// The external admin origin is emitted only after the same IP-literal validation
// used by the native connection, so it is safe to offer as a deliberate link.
type webAdGuardStatus struct {
	Connected         bool                        `json:"connected"`
	Endpoint          string                      `json:"endpoint,omitempty"`
	Version           string                      `json:"version,omitempty"`
	Running           bool                        `json:"running"`
	ProtectionEnabled bool                        `json:"protection_enabled"`
	FilteringEnabled  bool                        `json:"filtering_enabled"`
	QueryLogEnabled   bool                        `json:"query_log_enabled"`
	AnonymizedClients bool                        `json:"anonymized_clients"`
	FilterInventory   *api.AdGuardFilterInventory `json:"filter_inventory,omitempty"`
}

func projectWebAdGuardFilterInventory(inventory *api.AdGuardFilterInventory) *api.AdGuardFilterInventory {
	if inventory == nil {
		return nil
	}
	if inventory.BlocklistTotal < 0 || inventory.AllowlistTotal < 0 || inventory.BlocklistTotal > 100000 || inventory.AllowlistTotal > 100000 ||
		len(inventory.Sources) > 2*adguard.MaxFilterSources || inventory.Truncated != (inventory.BlocklistTotal > adguard.MaxFilterSources || inventory.AllowlistTotal > adguard.MaxFilterSources) {
		return nil
	}
	block, allow := 0, 0
	for _, source := range inventory.Sources {
		if source.Kind == "blocklist" {
			block++
		} else if source.Kind == "allowlist" {
			allow++
		} else {
			return nil
		}
		id, err := strconv.ParseInt(source.ID, 10, 64)
		if err != nil || strconv.FormatInt(id, 10) != source.ID || source.Name == "" || len(source.Name) > 128 ||
			!utf8.ValidString(source.Name) || strings.IndexFunc(source.Name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 ||
			(source.LastUpdated != nil && source.LastUpdated.IsZero()) {
			return nil
		}
	}
	if block != min(inventory.BlocklistTotal, adguard.MaxFilterSources) || allow != min(inventory.AllowlistTotal, adguard.MaxFilterSources) {
		return nil
	}
	projected := *inventory
	projected.Sources = append([]api.AdGuardFilterSource{}, inventory.Sources...)
	return &projected
}

func projectWebAdGuardStatus(status api.AdGuardConnection) (webAdGuardStatus, bool) {
	if !status.Connected {
		return webAdGuardStatus{Connected: false}, true
	}
	if _, err := adguard.NewClient(status.Endpoint, "", secretstore.Secret{}); err != nil {
		return webAdGuardStatus{}, false
	}
	if status.Version != adguard.SupportedVersion {
		return webAdGuardStatus{}, false
	}
	endpoint, err := url.Parse(status.Endpoint)
	if err != nil {
		return webAdGuardStatus{}, false
	}
	return webAdGuardStatus{Connected: true, Endpoint: endpoint.Scheme + "://" + endpoint.Host,
		Version: status.Version, Running: status.Running, ProtectionEnabled: status.ProtectionEnabled,
		FilteringEnabled: status.FilteringEnabled, QueryLogEnabled: status.QueryLogEnabled,
		AnonymizedClients: status.AnonymizedClients, FilterInventory: projectWebAdGuardFilterInventory(status.FilterInventory)}, true
}

func (h *webHandler) handleAdGuardStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", "AdGuard Home status is read-only")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "AdGuard Home status does not accept parameters or a body")
		return
	}
	if h.loadAdGuardStatus == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "AdGuard Home status is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	status, err := h.loadAdGuardStatus(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "AdGuard Home status is unavailable")
		return
	}
	projected, ok := projectWebAdGuardStatus(status)
	if !ok {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "AdGuard Home status is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, projected)
}
