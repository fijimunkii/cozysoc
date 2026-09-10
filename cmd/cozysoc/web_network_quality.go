package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

// This browser-specific projection deliberately excludes scope/sensor IDs,
// transient evidence IDs, native guidance, and any future native-only fields.
// All user-facing guidance is selected from the narrow enums in the frontend.
type webLocalQuality struct {
	Enrolled bool                `json:"enrolled"`
	AsOf     time.Time           `json:"as_of"`
	Observer *webQualityObserver `json:"observer,omitempty"`
	Check    *webInterfaceCheck  `json:"check,omitempty"`
}

type webQualityObserver struct {
	InterfaceName  string `json:"interface_name"`
	InterfaceIndex int    `json:"interface_index"`
}

type webInterfaceCheck struct {
	Source           string    `json:"source"`
	Layer            string    `json:"layer"`
	Method           string    `json:"method"`
	State            string    `json:"state"`
	Confidence       string    `json:"confidence"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at"`
	FreshUntil       time.Time `json:"fresh_until"`
	Gap              string    `json:"gap,omitempty"`
	AdministrativeUp *bool     `json:"administrative_up,omitempty"`
}

var qualityWebInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

func configureWebLocalQuality(h *webHandler, stateDir string) {
	h.loadLocalQuality = func(ctx context.Context) (api.LocalNetworkQuality, error) {
		result, err := localapi.NewClient(stateDir).Call(ctx, api.MethodNetworkQualityLocal)
		if err != nil {
			return api.LocalNetworkQuality{}, err
		}
		var quality api.LocalNetworkQuality
		if err := json.Unmarshal(result, &quality); err != nil {
			return api.LocalNetworkQuality{}, err
		}
		return quality, nil
	}
}

func projectWebLocalQuality(native api.LocalNetworkQuality) (webLocalQuality, error) {
	invalid := func() (webLocalQuality, error) {
		return webLocalQuality{}, fmt.Errorf("invalid local quality projection")
	}
	if native.AsOf.IsZero() || native.AsOf.Year() < 1 || native.AsOf.Year() > 9999 {
		return invalid()
	}
	result := webLocalQuality{Enrolled: native.Enrolled, AsOf: native.AsOf.UTC()}
	if !native.Enrolled {
		if native.Observer != nil || native.Check != nil {
			return invalid()
		}
		return result, nil
	}
	o, c := native.Observer, native.Check
	if o == nil || c == nil || !qualityWebInterfacePattern.MatchString(o.InterfaceName) || o.InterfaceIndex < 1 || o.InterfaceIndex > 2147483647 {
		return invalid()
	}
	if c.Source != "os-interface-metadata" || c.Layer != "local-link" || c.Method != "interface-state" {
		return invalid()
	}
	if c.StartedAt.IsZero() || c.StartedAt.After(c.CompletedAt) || !c.CompletedAt.Equal(native.AsOf) ||
		c.CompletedAt.Sub(c.StartedAt) > localInterfaceFreshness || !c.FreshUntil.Equal(c.CompletedAt.Add(localInterfaceFreshness)) || c.FreshUntil.Year() > 9999 {
		return invalid()
	}
	switch c.State {
	case "check-succeeded", "issue-observed":
		if c.Confidence != "limited" || c.Gap != "" || c.AdministrativeUp == nil || *c.AdministrativeUp != (c.State == "check-succeeded") {
			return invalid()
		}
	case "not-measured":
		if c.Confidence != "unknown" || c.AdministrativeUp != nil {
			return invalid()
		}
		switch c.Gap {
		case "network-changed", "permission-required", "unsupported", "source-unavailable":
		default:
			return invalid()
		}
	default:
		return invalid()
	}
	result.Observer = &webQualityObserver{InterfaceName: o.InterfaceName, InterfaceIndex: o.InterfaceIndex}
	result.Check = &webInterfaceCheck{
		Source: c.Source, Layer: c.Layer, Method: c.Method, State: c.State, Confidence: c.Confidence,
		StartedAt: c.StartedAt.UTC(), CompletedAt: c.CompletedAt.UTC(), FreshUntil: c.FreshUntil.UTC(), Gap: c.Gap,
	}
	if c.AdministrativeUp != nil {
		up := *c.AdministrativeUp
		result.Check.AdministrativeUp = &up
	}
	return result, nil
}

func (h *webHandler) handleLocalQuality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "local network quality") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "local network quality takes no query or body")
		return
	}
	unavailable := func() {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "local interface sample is unavailable")
	}
	if h.loadLocalQuality == nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadLocalQuality(ctx)
	if err != nil || ctx.Err() != nil {
		unavailable()
		return
	}
	result, err := projectWebLocalQuality(native)
	if err != nil {
		unavailable()
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}
