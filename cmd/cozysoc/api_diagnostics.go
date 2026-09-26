package main

import (
	"context"
	"regexp"
	"strings"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

var diagnosticBuildVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// DiagnosticsPreview constructs a support snapshot from known fields. No native
// response or error is copied wholesale into a diagnostic result.
func (h *controllerAPIHandler) DiagnosticsPreview(ctx context.Context) (api.DiagnosticPreview, error) {
	status := h.controller.Status()
	health := h.controller.Health()
	build := "redacted"
	if diagnosticBuildVersion.MatchString(status.ControllerVersion) {
		build = status.ControllerVersion
	}
	preview := api.DiagnosticPreview{
		SchemaVersion: 1,
		GeneratedAt:   h.now().UTC(),
		Controller: api.DiagnosticController{
			BuildVersion: build, ConfigSchemaVersion: status.ConfigSchemaVersion,
			HealthState: diagnosticControllerState(health.State), GapCount: health.GapCount,
		},
		Modules:  []api.DiagnosticModule{},
		Coverage: []api.DiagnosticCoverage{},
	}
	for _, instance := range h.controller.Capabilities().Capabilities {
		// The current support schema knows only the first-party Device Watch
		// module. A future integration must add an explicit projection here.
		if instance.Manifest.ID != "device-watch" {
			continue
		}
		preview.Modules = append(preview.Modules, api.DiagnosticModule{
			ID: "device-watch", BuildVersion: build,
			Desired:      diagnosticDesired(string(instance.State.Desired)),
			Verification: diagnosticVerification(string(instance.State.Verification)),
		})
		break
	}
	coverage := api.DiagnosticCoverage{CapabilityID: "device-watch", State: "unavailable", FailureCategory: "read-failed"}
	current, err := h.DeviceWatchCoverage(ctx)
	if err == nil {
		coverage.State = diagnosticCoverageState(current.State)
		coverage.FailureCategory = diagnosticCoverageFailure(current.State, current.Reason)
	} else if ctx.Err() != nil {
		return api.DiagnosticPreview{}, ctx.Err()
	}
	preview.Coverage = append(preview.Coverage, coverage)
	return preview, nil
}

func diagnosticControllerState(value string) string {
	switch value {
	case "ok", "degraded":
		return value
	default:
		return "unknown"
	}
}

func diagnosticDesired(value string) string {
	switch value {
	case "enabled", "disabled":
		return value
	default:
		return "unknown"
	}
}

func diagnosticVerification(value string) string {
	switch value {
	case "unverified", "verifying", "verified", "degraded", "stale":
		return value
	default:
		return "unknown"
	}
}

func diagnosticCoverageState(value string) string {
	switch value {
	case "unconfigured", "unavailable", "unverified", "active-limited", "degraded", "stale", "disconnected":
		return value
	default:
		return "unknown"
	}
}

func diagnosticCoverageFailure(state, reason string) string {
	switch state {
	case "unconfigured":
		return "not-configured"
	case "active-limited":
		return "none"
	}
	switch {
	case strings.HasPrefix(reason, "sensor-"):
		return "sensor"
	case strings.HasPrefix(reason, "ingestion-"):
		return "ingestion"
	case strings.HasPrefix(reason, "storage-"):
		return "storage"
	case reason == "no-evidence" || reason == "stale-evidence" || reason == "invalid-evidence" || reason == "clock-skew":
		return "evidence"
	case reason == "source-partial" || reason == "source-unavailable":
		return "source"
	default:
		return "unknown"
	}
}
