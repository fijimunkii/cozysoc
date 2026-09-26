package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func loadDiagnosticsFromController(ctx context.Context, stateDir string) (api.DiagnosticPreview, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	raw, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodDiagnosticsPreview)
	if err != nil {
		return api.DiagnosticPreview{}, fmt.Errorf("load diagnostic preview: %w", err)
	}
	var preview api.DiagnosticPreview
	if err := json.Unmarshal(raw, &preview); err != nil {
		return api.DiagnosticPreview{}, fmt.Errorf("decode diagnostic preview: %w", err)
	}
	return preview, nil
}

func validDiagnosticPreview(preview api.DiagnosticPreview) bool {
	if preview.SchemaVersion != 2 || preview.GeneratedAt.IsZero() ||
		!diagnosticBuildVersion.MatchString(preview.Controller.BuildVersion) ||
		preview.Controller.ConfigSchemaVersion < 1 ||
		preview.Controller.HealthState != diagnosticControllerState(preview.Controller.HealthState) ||
		len(preview.Modules) > 1 || len(preview.Coverage) != 1 || !validDiagnosticStorage(preview.Storage) {
		return false
	}
	for _, module := range preview.Modules {
		if module.ID != "device-watch" || module.BuildVersion != preview.Controller.BuildVersion ||
			module.Desired != diagnosticDesired(module.Desired) || module.Verification != diagnosticVerification(module.Verification) {
			return false
		}
	}
	coverage := preview.Coverage[0]
	if coverage.CapabilityID != "device-watch" || coverage.State != diagnosticCoverageState(coverage.State) {
		return false
	}
	switch coverage.FailureCategory {
	case "none", "not-configured", "read-failed", "sensor", "ingestion", "storage", "evidence", "source", "unknown":
		return true
	default:
		return false
	}
}

func validDiagnosticStorage(value api.DiagnosticStorage) bool {
	if value.ReadState != "current" && value.ReadState != "unavailable" {
		return false
	}
	switch value.QuotaState {
	case "current", "pressure", "at-quota", "unknown":
	default:
		return false
	}
	switch value.VolumeState {
	case "current", "pressure", "full", "unavailable", "unsupported", "unknown":
	default:
		return false
	}
	return value.ReadState != "unavailable" || value.QuotaState == "unknown" && value.VolumeState == "unknown"
}

func (h *webHandler) handleDiagnosticsPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "diagnostic preview") {
		return
	}
	if h.loadDiagnostics == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "diagnostic preview is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	preview, err := h.loadDiagnostics(ctx)
	if err != nil || !validDiagnosticPreview(preview) {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "diagnostic preview is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, preview)
}
