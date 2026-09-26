package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

// Browser settings are fixed, configuration-only controller operations. They
// cannot carry a source, run ticket, schedule, budget, or check consent.
func configureWebResolverSettings(h *webHandler, stateDir string) {
	client := localapi.NewClient(stateDir)
	h.saveResolverSettings = func(ctx context.Context, p api.ResolverSettingsParams) (api.ResolverSettingsResult, error) {
		raw, err := client.CallWithParams(ctx, api.MethodResolverSave, p)
		if err != nil {
			return api.ResolverSettingsResult{}, err
		}
		var result api.ResolverSettingsResult
		if len(raw) > 16*1024 || json.Unmarshal(raw, &result) != nil {
			return api.ResolverSettingsResult{}, errors.New("invalid resolver save response")
		}
		return result, nil
	}
	h.retireResolverSettings = func(ctx context.Context, p api.ResolverIDParams) (api.ResolverRetireResult, error) {
		raw, err := client.CallWithParams(ctx, api.MethodResolverRetire, p)
		if err != nil {
			return api.ResolverRetireResult{}, err
		}
		var result api.ResolverRetireResult
		if len(raw) > 1024 || json.Unmarshal(raw, &result) != nil {
			return api.ResolverRetireResult{}, errors.New("invalid resolver retirement response")
		}
		return result, nil
	}
}

// Reject duplicate, missing, and unknown fields before they reach the native
// controller; a browser mutation should have a single unambiguous meaning.
func decodeExactWebSettingsBody(w http.ResponseWriter, r *http.Request, fields map[string]any) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeWebError(w, 400, "invalid_request", "settings do not accept query parameters")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeWebError(w, 415, "content_type_required", "settings require JSON")
		return false
	}
	limited := http.MaxBytesReader(w, r.Body, 2048)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		writeWebError(w, 400, "invalid_request", "settings request is invalid")
		return false
	}
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			writeWebError(w, 400, "invalid_request", "settings request is invalid")
			return false
		}
		target, allowed := fields[name]
		if !allowed {
			writeWebError(w, 400, "invalid_request", "settings request is invalid")
			return false
		}
		seen[name] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || json.Unmarshal(raw, target) != nil || string(raw) == "null" {
			writeWebError(w, 400, "invalid_request", "settings request is invalid")
			return false
		}
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(seen) != len(fields) {
		writeWebError(w, 400, "invalid_request", "settings request is invalid")
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		writeWebError(w, 400, "invalid_request", "settings request is invalid")
		return false
	}
	return true
}

func (h *webHandler) handleResolverSelectionSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var p api.ResolverSettingsParams
	if !decodeExactWebSettingsBody(w, r, map[string]any{
		"endpoint": &p.Endpoint, "name": &p.Name, "family": &p.Family,
		"transport": &p.Transport, "query_type": &p.QueryType, "expect": &p.Expect,
		"destination_scope": &p.DestinationScope,
	}) {
		return
	}
	if _, err := resolverConfiguration(p); err != nil {
		writeWebError(w, 400, "invalid_settings", "enter an explicit supported resolver target and query")
		return
	}
	if h.saveResolverSettings == nil {
		writeWebError(w, 503, "mutation_unavailable", "saving resolver settings is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.saveResolverSettings(ctx, p)
	if err != nil {
		writeWebError(w, 503, "mutation_outcome_unknown", "reload saved selections before deciding whether to try again")
		return
	}
	projected, err := projectWebResolverSelections(native)
	p.Name = strings.ToLower(p.Name)
	if err != nil || len(projected.Items) != 1 || !reflect.DeepEqual(projected.Items[0].Settings, p) {
		writeWebError(w, 503, "mutation_outcome_unknown", "reload saved selections before deciding whether to try again")
		return
	}
	writeWebJSON(w, 200, projected.Items[0])
}

func (h *webHandler) handleResolverSelectionRetire(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var p api.ResolverIDParams
	if !decodeExactWebSettingsBody(w, r, map[string]any{"selection_id": &p.SelectionID}) {
		return
	}
	if !localapi.ValidResolverSelectionID(p.SelectionID) {
		writeWebError(w, 400, "invalid_selection", "choose a saved resolver selection")
		return
	}
	if h.retireResolverSettings == nil {
		writeWebError(w, 503, "mutation_unavailable", "retiring resolver settings is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	result, err := h.retireResolverSettings(ctx, p)
	if err != nil || result.SchemaVersion != 1 || result.SelectionID != p.SelectionID || result.State != "retired" {
		writeWebError(w, 503, "mutation_outcome_unknown", "reload saved selections before deciding whether to try again")
		return
	}
	writeWebJSON(w, 200, result)
}
