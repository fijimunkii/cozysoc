package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

type webStatus struct {
	ControllerVersion   string    `json:"controller_version"`
	StartedAt           time.Time `json:"started_at"`
	ConfigSchemaVersion int       `json:"config_schema_version"`
	Transport           string    `json:"transport"`
}

type webCapabilityState struct {
	Desired      string `json:"desired"`
	Process      string `json:"process"`
	Verification string `json:"verification"`
}

type webCapabilityTarget struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	MinVersion string `json:"min_version,omitempty"`
	Support    string `json:"support"`
}

type webCapabilityPrivilege struct {
	ID          string `json:"id"`
	Requirement string `json:"requirement"`
	Description string `json:"description"`
}

type webCapabilityResources struct {
	Measurement   string  `json:"measurement"`
	Profile       string  `json:"profile"`
	MaxRAMMiB     int     `json:"max_ram_mib,omitempty"`
	MaxDiskMiB    int     `json:"max_disk_mib,omitempty"`
	MaxCPUPercent float64 `json:"max_cpu_percent,omitempty"`
	Evidence      string  `json:"evidence,omitempty"`
}

type webCapabilityProvenance struct {
	Kind          string `json:"kind"`
	License       string `json:"license"`
	VersionPolicy string `json:"version_policy"`
}

type webCapabilityHealth struct {
	ProcessRequired              bool     `json:"process_required"`
	VerificationSignals          []string `json:"verification_signals"`
	CoverageRequiresVerification bool     `json:"coverage_requires_verification"`
}

type webCapability struct {
	ID            string                   `json:"id"`
	DisplayName   string                   `json:"display_name"`
	Summary       string                   `json:"summary"`
	Release       string                   `json:"release"`
	Configured    bool                     `json:"configured"`
	Ownership     string                   `json:"ownership"`
	State         webCapabilityState       `json:"state"`
	Targets       []webCapabilityTarget    `json:"targets"`
	Privileges    []webCapabilityPrivilege `json:"privileges"`
	Resources     webCapabilityResources   `json:"resources"`
	Provenance    webCapabilityProvenance  `json:"provenance"`
	Health        webCapabilityHealth      `json:"health"`
	Lifecycle     []string                 `json:"lifecycle"`
	DeepLinkCount int                      `json:"deep_link_count"`
}

type webCapabilityList struct {
	CatalogSchemaVersion int             `json:"catalog_schema_version"`
	Capabilities         []webCapability `json:"capabilities"`
}

func loadStatusFromController(ctx context.Context, stateDir string) (api.Status, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodStatus)
	if err != nil {
		return api.Status{}, fmt.Errorf("load controller status: %w", err)
	}
	var status api.Status
	if err := json.Unmarshal(result, &status); err != nil {
		return api.Status{}, fmt.Errorf("decode controller status: %w", err)
	}
	return status, nil
}

func loadCapabilitiesFromController(ctx context.Context, stateDir string) (api.CapabilityList, error) {
	requestCtx, cancel := context.WithTimeout(ctx, webRequestTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(requestCtx, api.MethodCapabilitiesList)
	if err != nil {
		return api.CapabilityList{}, fmt.Errorf("load controller capabilities: %w", err)
	}
	var capabilities api.CapabilityList
	if err := json.Unmarshal(result, &capabilities); err != nil {
		return api.CapabilityList{}, fmt.Errorf("decode controller capabilities: %w", err)
	}
	return capabilities, nil
}

func projectWebStatus(status api.Status) webStatus {
	return webStatus{
		ControllerVersion:   status.ControllerVersion,
		StartedAt:           status.StartedAt.UTC(),
		ConfigSchemaVersion: status.ConfigSchemaVersion,
		Transport:           status.Transport,
	}
}

func projectWebCapabilities(list api.CapabilityList) webCapabilityList {
	result := webCapabilityList{
		CatalogSchemaVersion: list.CatalogSchemaVersion,
		Capabilities:         make([]webCapability, 0, len(list.Capabilities)),
	}
	for _, instance := range list.Capabilities {
		manifest := instance.Manifest
		item := webCapability{
			ID:          manifest.ID,
			DisplayName: manifest.DisplayName,
			Summary:     manifest.Summary,
			Release:     manifest.Release,
			Configured:  instance.Configured,
			Ownership:   string(instance.Ownership),
			State: webCapabilityState{
				Desired:      string(instance.State.Desired),
				Process:      string(instance.State.Process),
				Verification: string(instance.State.Verification),
			},
			Targets:    make([]webCapabilityTarget, 0, len(manifest.Targets)),
			Privileges: make([]webCapabilityPrivilege, 0, len(manifest.Privileges)),
			Resources: webCapabilityResources{
				Measurement:   string(manifest.Resources.Measurement),
				Profile:       manifest.Resources.Profile,
				MaxRAMMiB:     manifest.Resources.MaxRAMMiB,
				MaxDiskMiB:    manifest.Resources.MaxDiskMiB,
				MaxCPUPercent: manifest.Resources.MaxCPUPercent,
				Evidence:      manifest.Resources.Evidence,
			},
			Provenance: webCapabilityProvenance{
				Kind:          manifest.Provenance.Kind,
				License:       manifest.Provenance.License,
				VersionPolicy: manifest.Provenance.VersionPolicy,
			},
			Health: webCapabilityHealth{
				ProcessRequired:              manifest.Health.ProcessRequired,
				VerificationSignals:          append([]string(nil), manifest.Health.VerificationSignals...),
				CoverageRequiresVerification: manifest.Health.CoverageRequiresVerification,
			},
			Lifecycle:     make([]string, 0, len(manifest.Lifecycle)),
			DeepLinkCount: len(manifest.DeepLinks),
		}
		for _, target := range manifest.Targets {
			item.Targets = append(item.Targets, webCapabilityTarget{
				OS: target.OS, Arch: target.Arch, MinVersion: target.MinVersion, Support: string(target.Support),
			})
		}
		for _, privilege := range manifest.Privileges {
			item.Privileges = append(item.Privileges, webCapabilityPrivilege{
				ID: privilege.ID, Requirement: string(privilege.Requirement), Description: privilege.Description,
			})
		}
		for _, action := range manifest.Lifecycle {
			item.Lifecycle = append(item.Lifecycle, string(action))
		}
		result.Capabilities = append(result.Capabilities, item)
	}
	return result
}

func (h *webHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "controller status") {
		return
	}
	if h.loadStatus == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "controller status is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	status, err := h.loadStatus(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "controller status is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, projectWebStatus(status))
}

func (h *webHandler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "capabilities") {
		return
	}
	if h.loadCapabilities == nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "capability information is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	capabilities, err := h.loadCapabilities(ctx)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "capability information is unavailable")
		return
	}
	writeWebJSON(w, http.StatusOK, projectWebCapabilities(capabilities))
}

func validateParameterlessWebRead(w http.ResponseWriter, r *http.Request, label string) bool {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeWebError(w, http.StatusMethodNotAllowed, "method_not_allowed", label+" is read-only")
		return false
	}
	if r.URL.RawQuery != "" {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", label+" does not accept query parameters")
		return false
	}
	if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		writeWebError(w, http.StatusBadRequest, "request_body_not_allowed", label+" does not accept a body")
		return false
	}
	return true
}
