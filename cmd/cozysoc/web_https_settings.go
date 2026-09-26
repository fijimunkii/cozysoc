package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func configureWebHTTPSSettings(h *webHandler, stateDir string) {
	client := localapi.NewClient(stateDir)
	h.saveHTTPSSettings = func(ctx context.Context, p api.HTTPSSettingsParams) (api.HTTPSSettingsResult, error) {
		raw, err := client.CallWithParams(ctx, api.MethodHTTPSSave, p)
		if err != nil {
			return api.HTTPSSettingsResult{}, err
		}
		var result api.HTTPSSettingsResult
		if len(raw) > 16*1024 || json.Unmarshal(raw, &result) != nil {
			return api.HTTPSSettingsResult{}, errors.New("invalid HTTPS save response")
		}
		return result, nil
	}
	h.retireHTTPSSettings = func(ctx context.Context, p api.HTTPSIDParams) (api.HTTPSRetireResult, error) {
		raw, err := client.CallWithParams(ctx, api.MethodHTTPSRetire, p)
		if err != nil {
			return api.HTTPSRetireResult{}, err
		}
		var result api.HTTPSRetireResult
		if len(raw) > 1024 || json.Unmarshal(raw, &result) != nil {
			return api.HTTPSRetireResult{}, errors.New("invalid HTTPS retirement response")
		}
		return result, nil
	}
}

func (h *webHandler) handleHTTPSSelectionSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var p api.HTTPSSettingsParams
	if !decodeExactWebSettingsBody(w, r, map[string]any{
		"endpoint": &p.Endpoint, "server_name": &p.ServerName, "request_target": &p.RequestTarget,
		"family": &p.Family, "method": &p.Method, "expected_status": &p.ExpectedStatus,
		"destination_policy": &p.DestinationPolicy,
	}) {
		return
	}
	if _, err := httpsConfiguration(p); err != nil {
		writeWebError(w, 400, "invalid_settings", "enter an explicit supported HTTPS endpoint, identity and request")
		return
	}
	if h.saveHTTPSSettings == nil {
		writeWebError(w, 503, "mutation_unavailable", "saving HTTPS settings is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.saveHTTPSSettings(ctx, p)
	if err != nil {
		writeWebError(w, 503, "mutation_outcome_unknown", "reload saved selections before deciding whether to try again")
		return
	}
	projected, err := projectWebHTTPSSelections(native, time.Now().UTC().Add(time.Second))
	p.ServerName = strings.ToLower(p.ServerName)
	if err != nil || len(projected.Items) != 1 || !reflect.DeepEqual(projected.Items[0].Settings, p) {
		writeWebError(w, 503, "mutation_outcome_unknown", "reload saved selections before deciding whether to try again")
		return
	}
	writeWebJSON(w, 200, projected.Items[0])
}

func (h *webHandler) handleHTTPSSelectionRetire(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var p api.HTTPSIDParams
	if !decodeExactWebSettingsBody(w, r, map[string]any{"selection_id": &p.SelectionID}) {
		return
	}
	if !localapi.ValidHTTPSSelectionID(p.SelectionID) {
		writeWebError(w, 400, "invalid_selection", "choose a saved HTTPS selection")
		return
	}
	if h.retireHTTPSSettings == nil {
		writeWebError(w, 503, "mutation_unavailable", "retiring HTTPS settings is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.retireHTTPSSettings(ctx, p)
	if err != nil || result.SchemaVersion != 1 || result.SelectionID != p.SelectionID || result.State != "retired" {
		writeWebError(w, 503, "mutation_outcome_unknown", "reload saved selections before deciding whether to try again")
		return
	}
	writeWebJSON(w, 200, result)
}
