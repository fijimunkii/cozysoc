package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type httpsSettingsLoader func(context.Context) (api.HTTPSSettingsResult, error)
type httpsPlanLoader func(context.Context, string) (api.HTTPSPlan, error)
type httpsCheckRunner func(context.Context, string, func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error)
type webHTTPSReviewState struct {
	id   string
	plan api.HTTPSPlan
}
type webHTTPSSelection struct {
	SelectionID string                  `json:"selection_id"`
	Settings    api.HTTPSSettingsParams `json:"settings"`
}
type webHTTPSSelections struct {
	Items []webHTTPSSelection `json:"items"`
}
type webHTTPSCheckReview struct {
	ReviewID                string                  `json:"review_id"`
	SelectionID             string                  `json:"selection_id"`
	Settings                api.HTTPSSettingsParams `json:"settings"`
	CreatedAt               time.Time               `json:"created_at"`
	ExpiresAt               time.Time               `json:"expires_at"`
	Source                  string                  `json:"source"`
	InterfaceName           string                  `json:"interface_name"`
	InterfaceIndex          int                     `json:"interface_index"`
	Prefixes                []string                `json:"prefixes"`
	OutsideEnrolledPrefixes bool                    `json:"outside_enrolled_prefixes"`
	RouteObservedAt         time.Time               `json:"route_observed_at"`
	RouteFreshUntil         time.Time               `json:"route_fresh_until"`
	Policy                  api.HTTPSPlanPolicy     `json:"policy"`
	RequestBytes            string                  `json:"request_bytes"`
	Privacy                 []string                `json:"privacy"`
	Budget                  api.HTTPSPlanBudget     `json:"budget"`
}
type webHTTPSCheckResult struct {
	Outcome     string `json:"outcome"`
	RunID       string `json:"run_id,omitempty"`
	FailureCode string `json:"failure_code,omitempty"`
}

var (
	webHTTPSEndpointIDPattern = regexp.MustCompile(`^https-endpoint\.[0-9a-f]{32}$`)
	webHTTPSRequestIDPattern  = regexp.MustCompile(`^https-request\.[0-9a-f]{32}$`)
	errWebHTTPSChanged        = errors.New("reviewed HTTPS check changed before approval")
)

func configureWebHTTPSCheck(h *webHandler, stateDir string) {
	client := localapi.NewClient(stateDir)
	h.loadHTTPSSettings = func(ctx context.Context) (api.HTTPSSettingsResult, error) {
		raw, err := client.Call(ctx, api.MethodHTTPSList)
		if err != nil {
			return api.HTTPSSettingsResult{}, err
		}
		var out api.HTTPSSettingsResult
		if len(raw) > 64*1024 || json.Unmarshal(raw, &out) != nil {
			return api.HTTPSSettingsResult{}, errors.New("invalid HTTPS selection list")
		}
		return out, nil
	}
	h.loadHTTPSPlan = func(ctx context.Context, id string) (api.HTTPSPlan, error) {
		raw, err := client.CallWithParams(ctx, api.MethodHTTPSPlan, api.HTTPSIDParams{SelectionID: id})
		if err != nil {
			return api.HTTPSPlan{}, err
		}
		var out api.HTTPSPlan
		if len(raw) > 64*1024 || json.Unmarshal(raw, &out) != nil {
			return api.HTTPSPlan{}, errors.New("invalid HTTPS review")
		}
		return out, nil
	}
	h.runHTTPSCheck = client.CheckHTTPS
}
func validWebHTTPSSettings(s api.HTTPSSettings, now time.Time) bool {
	if !localapi.ValidHTTPSSelectionID(s.SelectionID) || !webHTTPSEndpointIDPattern.MatchString(s.EndpointID) || !webHTTPSRequestIDPattern.MatchString(s.RequestID) ||
		s.Profile != httpsplan.Profile || len(s.ScopeID) == 0 || len(s.ScopeID) > 128 || s.CreatedAt.IsZero() || s.CreatedAt.After(now) {
		return false
	}
	endpoint, err := netip.ParseAddrPort(s.Settings.Endpoint)
	if err != nil || endpoint.String() != s.Settings.Endpoint {
		return false
	}
	c := httpsplan.Configuration{Endpoint: endpoint, ServerName: s.Settings.ServerName, RequestTarget: s.Settings.RequestTarget, DestinationPolicy: s.Settings.DestinationPolicy,
		Selection: nq.HTTPSSelection{ID: s.SelectionID, EndpointID: s.EndpointID, RequestID: s.RequestID, Family: nq.AddressFamily(s.Settings.Family), Method: s.Settings.Method, ExpectedStatus: s.Settings.ExpectedStatus}}
	return httpsplan.ValidateConfiguration(c) == nil && s.Settings.ServerName == strings.ToLower(s.Settings.ServerName)
}
func projectWebHTTPSSelections(native api.HTTPSSettingsResult, now time.Time) (webHTTPSSelections, error) {
	if native.SchemaVersion != 1 || native.Mode != "configuration-only" || native.ConsentGranted || native.Items == nil || len(native.Items) > 16 {
		return webHTTPSSelections{}, errors.New("invalid HTTPS selection list")
	}
	out := webHTTPSSelections{Items: make([]webHTTPSSelection, 0, len(native.Items))}
	seen := map[string]bool{}
	for _, s := range native.Items {
		if !validWebHTTPSSettings(s, now) || seen[s.SelectionID] {
			return webHTTPSSelections{}, errors.New("invalid HTTPS selection list")
		}
		seen[s.SelectionID] = true
		out.Items = append(out.Items, webHTTPSSelection{SelectionID: s.SelectionID, Settings: s.Settings})
	}
	return out, nil
}
func projectWebHTTPSCheckReview(p api.HTTPSPlan, id string, now time.Time) (webHTTPSCheckReview, error) {
	invalid := func() (webHTTPSCheckReview, error) { return webHTTPSCheckReview{}, errors.New("invalid HTTPS review") }
	if p.SchemaVersion != 1 || p.Mode != "preview-only" || p.ExecutionAvailable || p.ConsentGranted || p.Profile != httpsplan.Profile ||
		p.Configuration.SelectionID != id || !validWebHTTPSSettings(p.Configuration, now) || p.Configuration.ScopeID != p.Binding.ScopeID ||
		p.CreatedAt.After(now) || !now.Before(p.ExpiresAt) || !p.ExpiresAt.Equal(p.CreatedAt.Add(httpsplan.ReviewLifetime)) ||
		p.RouteObservedAt.After(p.CreatedAt) || !p.RouteFreshUntil.Equal(p.RouteObservedAt.Add(httpsplan.ReviewLifetime)) || !now.Before(p.RouteFreshUntil) {
		return invalid()
	}
	endpoint, err := netip.ParseAddrPort(p.Configuration.Settings.Endpoint)
	if err != nil {
		return invalid()
	}
	source, err := netip.ParseAddr(p.Source)
	if err != nil || source.String() != p.Source {
		return invalid()
	}
	s := p.Configuration
	canonical, err := httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: p.Binding.ScopeID, SensorID: p.SensorID, InterfaceName: p.Binding.InterfaceName, InterfaceIndex: p.Binding.InterfaceIndex}, Prefixes: p.Binding.Prefixes, Source: source},
		httpsplan.Configuration{Endpoint: endpoint, ServerName: s.Settings.ServerName, RequestTarget: s.Settings.RequestTarget, DestinationPolicy: s.Settings.DestinationPolicy,
			Selection: nq.HTTPSSelection{ID: s.SelectionID, EndpointID: s.EndpointID, RequestID: s.RequestID, Family: nq.AddressFamily(s.Settings.Family), Method: s.Settings.Method, ExpectedStatus: s.Settings.ExpectedStatus}}, p.CreatedAt)
	if err != nil {
		return invalid()
	}
	d := canonical.Disclosure()
	request, err := canonical.RequestBytes()
	if err != nil {
		return invalid()
	}
	if !slices.Equal(d.Binding.Prefixes, p.Binding.Prefixes) || d.OutsideEnrolledPrefixes != p.OutsideEnrolledPrefixes ||
		p.Policy != projectHTTPSPolicy(d.Policy) || p.Budget != projectHTTPSBudget(d.Budget) || p.RequestBytes != string(request) || !slices.Equal(p.Privacy, d.Privacy) {
		return invalid()
	}
	return webHTTPSCheckReview{SelectionID: id, Settings: s.Settings, CreatedAt: p.CreatedAt.UTC(), ExpiresAt: p.ExpiresAt.UTC(), Source: p.Source, InterfaceName: p.Binding.InterfaceName, InterfaceIndex: p.Binding.InterfaceIndex, Prefixes: slices.Clone(p.Binding.Prefixes), OutsideEnrolledPrefixes: p.OutsideEnrolledPrefixes, RouteObservedAt: p.RouteObservedAt.UTC(), RouteFreshUntil: p.RouteFreshUntil.UTC(), Policy: p.Policy, RequestBytes: p.RequestBytes, Privacy: slices.Clone(p.Privacy), Budget: p.Budget}, nil
}
func httpsReviewMatchesPlan(r api.HTTPSCheckReview, p api.HTTPSPlan, now time.Time) bool {
	expires := r.CreatedAt.Add(httpsplan.ReviewLifetime)
	if r.RouteFreshUntil.Before(expires) {
		expires = r.RouteFreshUntil
	}
	return r.SchemaVersion == 1 && r.Mode == "experimental-one-shot" && r.Profile == p.Profile && r.SelectionID == p.Configuration.SelectionID && r.EndpointID == p.Configuration.EndpointID && r.RequestID == p.Configuration.RequestID &&
		reflect.DeepEqual(r.Settings, p.Configuration.Settings) && reflect.DeepEqual(r.Binding, p.Binding) && r.SensorID == p.SensorID && r.Source == p.Source && r.OutsideEnrolledPrefixes == p.OutsideEnrolledPrefixes &&
		r.Policy == p.Policy && r.Budget == p.Budget && r.RequestBytes == p.RequestBytes && slices.Equal(r.Privacy, p.Privacy) &&
		!r.CreatedAt.Before(p.CreatedAt) && r.CreatedAt.Before(p.ExpiresAt) && !r.CreatedAt.After(now) && now.Before(p.RouteFreshUntil) && r.ExpiresAt.After(now) &&
		r.ExpiresAt.After(r.CreatedAt) && r.ExpiresAt.Equal(expires) &&
		!r.RouteObservedAt.Before(p.RouteObservedAt) && !r.RouteObservedAt.After(r.CreatedAt) && r.RouteFreshUntil.Equal(r.RouteObservedAt.Add(httpsplan.ReviewLifetime)) && now.Before(r.RouteFreshUntil)
}
func webHTTPSPlanCurrent(p api.HTTPSPlan, now time.Time) bool {
	return now.Before(p.ExpiresAt) && now.Before(p.RouteFreshUntil)
}
func (h *webHandler) handleHTTPSSelections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, 401, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "HTTPS selections") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, 400, "invalid_request", "HTTPS selections take no query or body")
		return
	}
	if h.loadHTTPSSettings == nil {
		writeWebError(w, 503, "check_unavailable", "saved HTTPS selections are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadHTTPSSettings(ctx)
	if err != nil || ctx.Err() != nil {
		writeWebError(w, 503, "check_unavailable", "saved HTTPS selections are unavailable")
		return
	}
	out, err := projectWebHTTPSSelections(native, time.Now().UTC())
	if err != nil {
		writeWebError(w, 503, "check_unavailable", "saved HTTPS selections are unavailable")
		return
	}
	writeWebJSON(w, 200, out)
}
func (h *webHandler) handleHTTPSReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		SelectionID string `json:"selection_id"`
	}
	if !decodeWebCheckBody(w, r, &input, "HTTPS") {
		return
	}
	if !localapi.ValidHTTPSSelectionID(input.SelectionID) {
		writeWebError(w, 400, "invalid_selection", "choose a saved HTTPS selection")
		return
	}
	if h.loadHTTPSPlan == nil {
		writeWebError(w, 503, "check_unavailable", "HTTPS review is unavailable")
		return
	}
	h.httpsReviewMu.Lock()
	busy := h.httpsReview != nil && webHTTPSPlanCurrent(h.httpsReview.plan, time.Now())
	h.httpsReviewMu.Unlock()
	if busy {
		writeWebError(w, 409, "review_pending", "finish or decline the current HTTPS review first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	p, err := h.loadHTTPSPlan(ctx, input.SelectionID)
	if err != nil || ctx.Err() != nil {
		writeWebError(w, 503, "check_unavailable", "HTTPS review is unavailable")
		return
	}
	preview, err := projectWebHTTPSCheckReview(p, input.SelectionID, time.Now().UTC())
	if err != nil {
		writeWebError(w, 412, "review_unavailable", "HTTPS binding or selection could not be verified")
		return
	}
	id, err := newWebToken()
	if err != nil {
		writeWebError(w, 503, "check_unavailable", "HTTPS review is unavailable")
		return
	}
	h.httpsReviewMu.Lock()
	if h.httpsReview != nil && webHTTPSPlanCurrent(h.httpsReview.plan, time.Now()) {
		h.httpsReviewMu.Unlock()
		writeWebError(w, 409, "review_pending", "finish or decline the current HTTPS review first")
		return
	}
	h.httpsReview = &webHTTPSReviewState{id: id, plan: p}
	h.httpsReviewMu.Unlock()
	preview.ReviewID = id
	writeWebJSON(w, 200, preview)
}
func (h *webHandler) handleHTTPSRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		ReviewID string `json:"review_id"`
		Approve  *bool  `json:"approve"`
	}
	if !decodeWebCheckBody(w, r, &input, "HTTPS") {
		return
	}
	if !webGatewayReviewIDPattern.MatchString(input.ReviewID) || input.Approve == nil {
		writeWebError(w, 400, "invalid_request", "HTTPS decision is invalid")
		return
	}
	h.httpsReviewMu.Lock()
	pending := h.httpsReview
	if pending == nil || len(pending.id) != len(input.ReviewID) || subtle.ConstantTimeCompare([]byte(pending.id), []byte(input.ReviewID)) != 1 || !webHTTPSPlanCurrent(pending.plan, time.Now()) {
		h.httpsReviewMu.Unlock()
		writeWebError(w, 409, "review_expired", "HTTPS review is unavailable or expired; no check was approved")
		return
	}
	h.httpsReview = nil
	h.httpsReviewMu.Unlock()
	if !*input.Approve {
		writeWebJSON(w, 200, webHTTPSCheckResult{Outcome: "declined"})
		return
	}
	if h.runHTTPSCheck == nil {
		writeWebError(w, 503, "check_unavailable", "HTTPS checks require experimental controller opt-in")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	approved := false
	var nativeReview api.HTTPSCheckReview
	result, err := h.runHTTPSCheck(ctx, pending.plan.Configuration.SelectionID, func(decisionCtx context.Context, review api.HTTPSCheckReview) (bool, error) {
		if decisionCtx.Err() != nil || !webHTTPSPlanCurrent(pending.plan, time.Now()) || !httpsReviewMatchesPlan(review, pending.plan, time.Now().UTC()) {
			return false, errWebHTTPSChanged
		}
		nativeReview = review
		approved = true
		return true, nil
	})
	if err != nil || ctx.Err() != nil {
		if approved {
			writeWebError(w, 503, "outcome_unknown", "approval may have sent HTTPS traffic; inspect saved history and do not automatically retry")
			return
		}
		if errors.Is(err, errWebHTTPSChanged) {
			writeWebError(w, 409, "review_changed", "HTTPS review changed before approval; no check was authorized")
			return
		}
		writeWebError(w, 503, "check_unavailable", "HTTPS check was unavailable before approval")
		return
	}
	if !approved {
		writeWebError(w, 409, "review_changed", "the reviewed check was not approved")
		return
	}
	if result.SchemaVersion != 1 || !reflect.DeepEqual(result.Review, nativeReview) || !httpsrun.ValidRunID(result.RunID) {
		writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
		return
	}
	switch result.Outcome {
	case "completed":
		if result.FailureCode != "" || result.Measurement == nil {
			writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
			return
		}
	case "failed":
		if result.FailureCode != "execution_failed" {
			writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
			return
		}
	case "canceled":
		if result.FailureCode != "canceled" {
			writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
			return
		}
	case "indeterminate":
		if result.FailureCode != "execution_failed" || result.Measurement != nil {
			writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
			return
		}
	case "blocked":
		if result.FailureCode != "precondition_failed" && result.FailureCode != "review_expired" {
			writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
			return
		}
	default:
		writeWebError(w, 503, "outcome_unknown", "result could not be verified; inspect saved history and do not automatically retry")
		return
	}
	writeWebJSON(w, 200, webHTTPSCheckResult{Outcome: result.Outcome, RunID: result.RunID, FailureCode: result.FailureCode})
}
