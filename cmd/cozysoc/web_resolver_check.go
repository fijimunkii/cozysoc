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
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

type resolverSettingsLoader func(context.Context) (api.ResolverSettingsResult, error)
type resolverPlanLoader func(context.Context, string) (api.ResolverPlan, error)
type resolverCheckRunner func(context.Context, string, func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error)
type webResolverReviewState struct {
	id   string
	plan api.ResolverPlan
}
type webResolverSelection struct {
	SelectionID string                     `json:"selection_id"`
	Settings    api.ResolverSettingsParams `json:"settings"`
}
type webResolverSelections struct {
	Items []webResolverSelection `json:"items"`
}
type webResolverReview struct {
	ReviewID                string                     `json:"review_id"`
	SelectionID             string                     `json:"selection_id"`
	Settings                api.ResolverSettingsParams `json:"settings"`
	CreatedAt               time.Time                  `json:"created_at"`
	ExpiresAt               time.Time                  `json:"expires_at"`
	Source                  string                     `json:"source"`
	InterfaceName           string                     `json:"interface_name"`
	InterfaceIndex          int                        `json:"interface_index"`
	Prefixes                []string                   `json:"prefixes"`
	OutsideEnrolledPrefixes bool                       `json:"outside_enrolled_prefixes"`
	MayForwardUpstream      bool                       `json:"may_forward_upstream"`
	Budget                  api.ResolverPlanBudget     `json:"budget"`
}
type webResolverRunResult struct {
	Outcome     string `json:"outcome"`
	RunID       string `json:"run_id,omitempty"`
	FailureCode string `json:"failure_code,omitempty"`
}

var (
	webResolverResolverIDPattern = regexp.MustCompile(`^resolver\.[0-9a-f]{32}$`)
	webResolverQueryIDPattern    = regexp.MustCompile(`^query\.[0-9a-f]{32}$`)
	errWebResolverChanged        = errors.New("reviewed resolver check changed before approval")
)

func configureWebResolverCheck(h *webHandler, stateDir string) {
	client := localapi.NewClient(stateDir)
	h.loadResolverSettings = func(ctx context.Context) (api.ResolverSettingsResult, error) {
		raw, err := client.Call(ctx, api.MethodResolverList)
		if err != nil {
			return api.ResolverSettingsResult{}, err
		}
		var out api.ResolverSettingsResult
		if len(raw) > 64*1024 || json.Unmarshal(raw, &out) != nil {
			return api.ResolverSettingsResult{}, errors.New("invalid resolver selection list")
		}
		return out, nil
	}
	h.loadResolverPlan = func(ctx context.Context, id string) (api.ResolverPlan, error) {
		raw, err := client.CallWithParams(ctx, api.MethodResolverPlan, api.ResolverIDParams{SelectionID: id})
		if err != nil {
			return api.ResolverPlan{}, err
		}
		var out api.ResolverPlan
		if len(raw) > 16*1024 || json.Unmarshal(raw, &out) != nil {
			return api.ResolverPlan{}, errors.New("invalid resolver review")
		}
		return out, nil
	}
	h.runResolverCheck = client.CheckResolver
}

func validWebResolverSettings(s api.ResolverSettings) bool {
	if !localapi.ValidResolverSelectionID(s.SelectionID) || !webResolverResolverIDPattern.MatchString(s.ResolverID) || !webResolverQueryIDPattern.MatchString(s.QueryID) ||
		len(s.ScopeID) == 0 || len(s.ScopeID) > 128 || s.CreatedAt.IsZero() || s.CreatedAt.After(time.Now().Add(time.Second)) {
		return false
	}
	endpoint, err := netip.ParseAddrPort(s.Settings.Endpoint)
	if err != nil || endpoint.String() != s.Settings.Endpoint {
		return false
	}
	c := resolverplan.Configuration{Endpoint: endpoint, Name: s.Settings.Name, DestinationScope: resolverplan.DestinationScope(s.Settings.DestinationScope),
		Selection: nq.ResolverSelection{ID: s.SelectionID, ResolverID: s.ResolverID, QueryID: s.QueryID, Family: nq.AddressFamily(s.Settings.Family), Transport: nq.DNSTransport(s.Settings.Transport), QueryType: nq.DNSQueryType(s.Settings.QueryType), Expect: nq.DNSExpectation(s.Settings.Expect)}}
	return resolverplan.ValidateConfiguration(c) == nil
}
func projectWebResolverSelections(native api.ResolverSettingsResult) (webResolverSelections, error) {
	if native.SchemaVersion != 1 || native.Mode != "configuration-only" || native.ConsentGranted || native.Items == nil || len(native.Items) > 16 {
		return webResolverSelections{}, errors.New("invalid resolver selection list")
	}
	out := webResolverSelections{Items: make([]webResolverSelection, 0, len(native.Items))}
	seen := map[string]bool{}
	for _, s := range native.Items {
		if !validWebResolverSettings(s) || seen[s.SelectionID] {
			return webResolverSelections{}, errors.New("invalid resolver selection list")
		}
		seen[s.SelectionID] = true
		out.Items = append(out.Items, webResolverSelection{SelectionID: s.SelectionID, Settings: s.Settings})
	}
	return out, nil
}
func projectWebResolverReview(plan api.ResolverPlan, id string, now time.Time) (webResolverReview, error) {
	invalid := func() (webResolverReview, error) { return webResolverReview{}, errors.New("invalid resolver review") }
	if plan.SchemaVersion != 1 || plan.Mode != "preview-only" || plan.ExecutionAvailable || plan.ConsentGranted || plan.Profile != resolverplan.Profile ||
		plan.Configuration.SelectionID != id || !validWebResolverSettings(plan.Configuration) || plan.Configuration.ScopeID != plan.Binding.ScopeID ||
		plan.CreatedAt.After(now) || !now.Before(plan.ExpiresAt) || !plan.ExpiresAt.Equal(plan.CreatedAt.Add(resolverplan.ReviewLifetime)) || !plan.MayForwardUpstream {
		return invalid()
	}
	endpoint, err := netip.ParseAddrPort(plan.Configuration.Settings.Endpoint)
	if err != nil {
		return invalid()
	}
	source, err := netip.ParseAddr(plan.Source)
	if err != nil || source.String() != plan.Source {
		return invalid()
	}
	s := plan.Configuration
	canonical, err := resolverplan.New(resolverplan.Binding{Observer: nq.Observer{ScopeID: plan.Binding.ScopeID, SensorID: plan.SensorID, InterfaceName: plan.Binding.InterfaceName, InterfaceIndex: plan.Binding.InterfaceIndex}, Prefixes: plan.Binding.Prefixes, Source: source},
		resolverplan.Configuration{Endpoint: endpoint, Name: s.Settings.Name, DestinationScope: resolverplan.DestinationScope(s.Settings.DestinationScope), Selection: nq.ResolverSelection{ID: s.SelectionID, ResolverID: s.ResolverID, QueryID: s.QueryID, Family: nq.AddressFamily(s.Settings.Family), Transport: nq.DNSTransport(s.Settings.Transport), QueryType: nq.DNSQueryType(s.Settings.QueryType), Expect: nq.DNSExpectation(s.Settings.Expect)}}, plan.CreatedAt)
	if err != nil {
		return invalid()
	}
	d := canonical.Disclosure()
	b := d.Budget
	expectedBudget := api.ResolverPlanBudget{MaxSendCalls: b.MaxSendCalls, MaxRequestBytes: b.MaxRequestBytes, MaxReplyBytes: b.MaxReplyBytes, MaxReceivedDatagrams: b.MaxReceivedDatagrams, MaxReceiveCalls: b.MaxReceiveCalls, ExchangeTimeoutMS: b.ExchangeTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(), MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()}
	if d.Configuration.Name != s.Settings.Name || !reflect.DeepEqual(d.Binding.Prefixes, plan.Binding.Prefixes) || d.OutsideEnrolledPrefixes != plan.OutsideEnrolledPrefixes || plan.Budget != expectedBudget {
		return invalid()
	}
	return webResolverReview{SelectionID: id, Settings: s.Settings, CreatedAt: plan.CreatedAt.UTC(), ExpiresAt: plan.ExpiresAt.UTC(), Source: plan.Source, InterfaceName: plan.Binding.InterfaceName, InterfaceIndex: plan.Binding.InterfaceIndex, Prefixes: append([]string(nil), plan.Binding.Prefixes...), OutsideEnrolledPrefixes: plan.OutsideEnrolledPrefixes, MayForwardUpstream: true, Budget: plan.Budget}, nil
}
func resolverReviewMatchesPlan(r api.ResolverCheckReview, p api.ResolverPlan) bool {
	return r.SchemaVersion == 1 && r.Mode == "experimental-one-shot" && r.Profile == p.Profile &&
		r.SelectionID == p.Configuration.SelectionID && r.ResolverID == p.Configuration.ResolverID && r.QueryID == p.Configuration.QueryID &&
		reflect.DeepEqual(r.Settings, p.Configuration.Settings) && reflect.DeepEqual(r.Binding, p.Binding) && r.SensorID == p.SensorID && r.Source == p.Source &&
		r.OutsideEnrolledPrefixes == p.OutsideEnrolledPrefixes && r.MayForwardUpstream == p.MayForwardUpstream && r.Budget == p.Budget &&
		!r.CreatedAt.Before(p.CreatedAt) && r.CreatedAt.Before(p.ExpiresAt) && r.ExpiresAt.After(r.CreatedAt) && !r.ExpiresAt.After(r.CreatedAt.Add(resolverplan.ReviewLifetime))
}
func (h *webHandler) handleResolverSelections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, 401, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "resolver selections") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, 400, "invalid_request", "resolver selections take no query or body")
		return
	}
	if h.loadResolverSettings == nil {
		writeWebError(w, 503, "check_unavailable", "saved resolver selections are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadResolverSettings(ctx)
	if err != nil || ctx.Err() != nil {
		writeWebError(w, 503, "check_unavailable", "saved resolver selections are unavailable")
		return
	}
	out, err := projectWebResolverSelections(native)
	if err != nil {
		writeWebError(w, 503, "check_unavailable", "saved resolver selections are unavailable")
		return
	}
	writeWebJSON(w, 200, out)
}
func (h *webHandler) handleResolverReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		SelectionID string `json:"selection_id"`
	}
	if !decodeWebCheckBody(w, r, &input, "resolver") {
		return
	}
	if !localapi.ValidResolverSelectionID(input.SelectionID) {
		writeWebError(w, 400, "invalid_selection", "choose a saved resolver selection")
		return
	}
	if h.loadResolverPlan == nil {
		writeWebError(w, 503, "check_unavailable", "resolver review is unavailable")
		return
	}
	h.resolverReviewMu.Lock()
	busy := h.resolverReview != nil && time.Now().Before(h.resolverReview.plan.ExpiresAt)
	h.resolverReviewMu.Unlock()
	if busy {
		writeWebError(w, 409, "review_pending", "finish or decline the current resolver review first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	plan, err := h.loadResolverPlan(ctx, input.SelectionID)
	if err != nil || ctx.Err() != nil {
		writeWebError(w, 503, "check_unavailable", "resolver review is unavailable")
		return
	}
	preview, err := projectWebResolverReview(plan, input.SelectionID, time.Now().UTC())
	if err != nil {
		writeWebError(w, 412, "review_unavailable", "resolver binding or selection could not be verified")
		return
	}
	id, err := newWebToken()
	if err != nil {
		writeWebError(w, 503, "check_unavailable", "resolver review is unavailable")
		return
	}
	h.resolverReviewMu.Lock()
	if h.resolverReview != nil && time.Now().Before(h.resolverReview.plan.ExpiresAt) {
		h.resolverReviewMu.Unlock()
		writeWebError(w, 409, "review_pending", "finish or decline the current resolver review first")
		return
	}
	h.resolverReview = &webResolverReviewState{id: id, plan: plan}
	h.resolverReviewMu.Unlock()
	preview.ReviewID = id
	writeWebJSON(w, 200, preview)
}
func (h *webHandler) handleResolverRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		ReviewID string `json:"review_id"`
		Approve  *bool  `json:"approve"`
	}
	if !decodeWebCheckBody(w, r, &input, "resolver") {
		return
	}
	if !webGatewayReviewIDPattern.MatchString(input.ReviewID) || input.Approve == nil {
		writeWebError(w, 400, "invalid_request", "resolver decision is invalid")
		return
	}
	h.resolverReviewMu.Lock()
	pending := h.resolverReview
	if pending == nil || len(pending.id) != len(input.ReviewID) || subtle.ConstantTimeCompare([]byte(pending.id), []byte(input.ReviewID)) != 1 || !time.Now().Before(pending.plan.ExpiresAt) {
		h.resolverReviewMu.Unlock()
		writeWebError(w, 409, "review_expired", "resolver review is unavailable or expired; no check was approved")
		return
	}
	h.resolverReview = nil
	h.resolverReviewMu.Unlock()
	if !*input.Approve {
		writeWebJSON(w, 200, webResolverRunResult{Outcome: "declined"})
		return
	}
	if h.runResolverCheck == nil {
		writeWebError(w, 503, "check_unavailable", "resolver checks require experimental controller opt-in")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	approved := false
	var nativeReview api.ResolverCheckReview
	result, err := h.runResolverCheck(ctx, pending.plan.Configuration.SelectionID, func(decisionCtx context.Context, r api.ResolverCheckReview) (bool, error) {
		if decisionCtx.Err() != nil || !time.Now().Before(pending.plan.ExpiresAt) || !resolverReviewMatchesPlan(r, pending.plan) {
			return false, errWebResolverChanged
		}
		nativeReview = r
		approved = true
		return true, nil
	})
	if err != nil || ctx.Err() != nil {
		if approved {
			writeWebError(w, 503, "outcome_unknown", "approval may have sent DNS traffic; inspect saved history and do not automatically retry")
			return
		}
		if errors.Is(err, errWebResolverChanged) {
			writeWebError(w, 409, "review_changed", "resolver review changed before approval; no check was authorized")
			return
		}
		writeWebError(w, 503, "check_unavailable", "resolver check was unavailable before approval")
		return
	}
	if !approved {
		writeWebError(w, 409, "review_changed", "the reviewed check was not approved")
		return
	}
	if result.SchemaVersion != 1 || !reflect.DeepEqual(result.Review, nativeReview) || !resolverrun.ValidRunID(result.RunID) {
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
	writeWebJSON(w, 200, webResolverRunResult{Outcome: result.Outcome, RunID: result.RunID, FailureCode: result.FailureCode})
}
