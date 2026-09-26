package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type gatewayPlanLoader func(context.Context, string) (api.GatewayCheckPlan, error)
type gatewayCheckRunner func(context.Context, string, func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error)

type webGatewayReviewState struct {
	id   string
	plan api.GatewayCheckPlan
}

type webGatewayReview struct {
	ReviewID       string                `json:"review_id"`
	CreatedAt      time.Time             `json:"created_at"`
	ExpiresAt      time.Time             `json:"expires_at"`
	Target         string                `json:"target"`
	Source         string                `json:"source"`
	InterfaceName  string                `json:"interface_name"`
	InterfaceIndex int                   `json:"interface_index"`
	Prefixes       []string              `json:"prefixes"`
	Budget         api.GatewayPlanBudget `json:"budget"`
}

type webGatewayMeasurement struct {
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	SendCalls        int        `json:"send_calls"`
	AcceptedRequests int        `json:"accepted_requests"`
	Replies          int        `json:"replies"`
	Timeouts         int        `json:"timeouts"`
	Complete         bool       `json:"complete"`
	MeanRTTNS        *int64     `json:"mean_rtt_ns,omitempty"`
}

type webGatewayRunResult struct {
	Outcome     string                 `json:"outcome"`
	RunID       string                 `json:"run_id,omitempty"`
	FailureCode string                 `json:"failure_code,omitempty"`
	Measurement *webGatewayMeasurement `json:"measurement,omitempty"`
}

var (
	webGatewayReviewIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	errWebGatewayChanged      = errors.New("reviewed gateway check changed before approval")
)

func configureWebGatewayCheck(h *webHandler, stateDir string) {
	client := localapi.NewClient(stateDir)
	h.loadGatewayPlan = func(ctx context.Context, target string) (api.GatewayCheckPlan, error) {
		raw, err := client.CallWithParams(ctx, api.MethodNetworkQualityGatewayPlan, api.GatewayPlanParams{Target: target})
		if err != nil {
			return api.GatewayCheckPlan{}, err
		}
		var plan api.GatewayCheckPlan
		if err := json.Unmarshal(raw, &plan); err != nil {
			return api.GatewayCheckPlan{}, err
		}
		return plan, nil
	}
	h.runGatewayCheck = client.CheckGateway
}

func projectWebGatewayReview(plan api.GatewayCheckPlan, now time.Time) (webGatewayReview, error) {
	invalid := func() (webGatewayReview, error) { return webGatewayReview{}, errors.New("invalid gateway review") }
	if plan.SchemaVersion != 1 || plan.Mode != "preview-only" || plan.ExecutionAvailable || plan.ConsentGranted ||
		plan.Method != "icmp-echo" || plan.Target.Family != "ipv4" || plan.Target.Role != "user-selected-gateway" || plan.Target.RoleVerified ||
		plan.CreatedAt.After(now) || !now.Before(plan.ReviewExpiresAt) {
		return invalid()
	}
	expected, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{
		ScopeID: plan.Binding.ScopeID, InterfaceName: plan.Binding.InterfaceName,
		InterfaceIndex: plan.Binding.InterfaceIndex, Prefixes: plan.Binding.Prefixes,
	}, plan.Target.Address, plan.CreatedAt)
	if err != nil || !plan.ReviewExpiresAt.Equal(expected.ReviewExpiresAt) ||
		!reflect.DeepEqual(plan.Binding.Prefixes, expected.Binding.Prefixes) ||
		plan.ProposedBudget != projectGatewayCheckPlan(expected).ProposedBudget {
		return invalid()
	}
	route := plan.Route
	if route.State != "consistent" || route.Reason != "" || route.Source != "darwin-rtm-get" || route.SendBindingVerified ||
		route.ObservedAt == nil || route.FreshUntil == nil || route.ObservedAt.Before(plan.CreatedAt) || route.ObservedAt.After(now) ||
		!route.FreshUntil.Equal(route.ObservedAt.Add(networkquality.GatewayReviewLifetime)) || !now.Before(*route.FreshUntil) {
		return invalid()
	}
	source, err := netip.ParseAddr(route.SourceAddress)
	if err != nil || !source.Is4() || !source.IsPrivate() || source.String() != route.SourceAddress || route.SourceAddress == plan.Target.Address {
		return invalid()
	}
	inside := false
	for _, raw := range plan.Binding.Prefixes {
		prefix, _ := netip.ParsePrefix(raw) // PreviewGatewayCheck already validated every prefix.
		inside = inside || prefix.Contains(source)
	}
	if !inside {
		return invalid()
	}
	return webGatewayReview{
		CreatedAt: plan.CreatedAt.UTC(), ExpiresAt: plan.ReviewExpiresAt.UTC(),
		Target: plan.Target.Address, Source: route.SourceAddress,
		InterfaceName: plan.Binding.InterfaceName, InterfaceIndex: plan.Binding.InterfaceIndex,
		Prefixes: append([]string(nil), plan.Binding.Prefixes...), Budget: plan.ProposedBudget,
	}, nil
}

func gatewayReviewMatchesPlan(review api.GatewayCheckReview, plan api.GatewayCheckPlan) bool {
	return review.SchemaVersion == 1 && review.Mode == "experimental-one-shot" && !review.GatewayRoleVerified &&
		review.Profile == gatewayrun.Profile && review.Target == plan.Target.Address && review.Source == plan.Route.SourceAddress &&
		reflect.DeepEqual(review.Binding, plan.Binding) && review.Budget == plan.ProposedBudget &&
		!review.CreatedAt.Before(plan.CreatedAt) && review.CreatedAt.Before(plan.ReviewExpiresAt) &&
		review.ExpiresAt.Equal(review.CreatedAt.Add(networkquality.GatewayReviewLifetime))
}

func (h *webHandler) handleGatewayReview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		Target string `json:"target"`
	}
	if !decodeWebCheckBody(w, r, &input, "gateway") {
		return
	}
	if networkquality.ValidateGatewayPreviewTarget(input.Target) != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_target", "choose one numeric private IPv4 host in the enrolled network")
		return
	}
	if h.loadGatewayPlan == nil {
		writeWebError(w, http.StatusServiceUnavailable, "check_unavailable", "gateway check review is unavailable")
		return
	}
	h.gatewayReviewMu.Lock()
	busy := h.gatewayReview != nil && time.Now().Before(h.gatewayReview.plan.ReviewExpiresAt)
	h.gatewayReviewMu.Unlock()
	if busy {
		writeWebError(w, http.StatusConflict, "review_pending", "finish or decline the current gateway review first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	plan, err := h.loadGatewayPlan(ctx, input.Target)
	if err != nil || ctx.Err() != nil {
		writeGatewayPlanError(w, err)
		return
	}
	preview, err := projectWebGatewayReview(plan, time.Now().UTC())
	if err != nil {
		writeWebError(w, http.StatusPreconditionFailed, "route_unavailable", "the current route and source could not be verified for this review")
		return
	}
	id, err := newWebToken()
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "check_unavailable", "gateway check review is unavailable")
		return
	}
	h.gatewayReviewMu.Lock()
	if h.gatewayReview != nil && time.Now().Before(h.gatewayReview.plan.ReviewExpiresAt) {
		h.gatewayReviewMu.Unlock()
		writeWebError(w, http.StatusConflict, "review_pending", "finish or decline the current gateway review first")
		return
	}
	h.gatewayReview = &webGatewayReviewState{id: id, plan: plan}
	h.gatewayReviewMu.Unlock()
	preview.ReviewID = id
	writeWebJSON(w, http.StatusOK, preview)
}

func (h *webHandler) handleGatewayRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorizeMutation(w, r) {
		return
	}
	var input struct {
		ReviewID string `json:"review_id"`
		Approve  *bool  `json:"approve"`
	}
	if !decodeWebCheckBody(w, r, &input, "gateway") {
		return
	}
	if !webGatewayReviewIDPattern.MatchString(input.ReviewID) || input.Approve == nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "gateway decision is invalid")
		return
	}
	h.gatewayReviewMu.Lock()
	pending := h.gatewayReview
	if pending == nil || len(pending.id) != len(input.ReviewID) || subtle.ConstantTimeCompare([]byte(pending.id), []byte(input.ReviewID)) != 1 || !time.Now().Before(pending.plan.ReviewExpiresAt) {
		h.gatewayReviewMu.Unlock()
		writeWebError(w, http.StatusConflict, "review_expired", "the gateway review is unavailable or expired; no check was approved")
		return
	}
	h.gatewayReview = nil // A decision consumes the review before any controller call.
	h.gatewayReviewMu.Unlock()
	if !*input.Approve {
		writeWebJSON(w, http.StatusOK, webGatewayRunResult{Outcome: "declined"})
		return
	}
	if h.runGatewayCheck == nil {
		writeWebError(w, http.StatusServiceUnavailable, "check_unavailable", "gateway checks require the experimental controller opt-in")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	approved := false
	result, err := h.runGatewayCheck(ctx, pending.plan.Target.Address, func(decisionCtx context.Context, review api.GatewayCheckReview) (bool, error) {
		if decisionCtx.Err() != nil || !time.Now().Before(pending.plan.ReviewExpiresAt) || !gatewayReviewMatchesPlan(review, pending.plan) {
			return false, errWebGatewayChanged
		}
		approved = true
		return true, nil
	})
	if err != nil || ctx.Err() != nil {
		if approved {
			writeWebError(w, http.StatusServiceUnavailable, "outcome_unknown", "approval may have sent traffic; inspect saved history and do not automatically retry")
		} else if errors.Is(err, errWebGatewayChanged) {
			writeWebError(w, http.StatusConflict, "review_changed", "the route or review changed before approval; no check was authorized")
		} else {
			writeGatewayRunError(w, err)
		}
		return
	}
	if !approved {
		writeWebError(w, http.StatusConflict, "review_changed", "the reviewed check was not approved")
		return
	}
	projected, err := projectWebGatewayResult(result, pending.plan, time.Now().UTC())
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "outcome_unknown", "the result could not be verified; inspect saved history and do not automatically retry")
		return
	}
	writeWebJSON(w, http.StatusOK, projected)
}

func decodeWebCheckBody(w http.ResponseWriter, r *http.Request, target any, check string) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeWebError(w, http.StatusBadRequest, "query_not_allowed", check+" requests take no query parameters")
		return false
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeWebError(w, http.StatusUnsupportedMediaType, "content_type_required", check+" requests require JSON")
		return false
	}
	limited := http.MaxBytesReader(w, r.Body, maxWebMutationBodyBytes)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || ensureJSONEOF(decoder) != nil {
		writeWebError(w, http.StatusBadRequest, "invalid_request", check+" request is invalid")
		return false
	}
	return true
}

func writeGatewayPlanError(w http.ResponseWriter, err error) {
	var response *localapi.ResponseError
	if errors.As(err, &response) {
		switch response.Code {
		case "invalid_request":
			writeWebError(w, http.StatusBadRequest, "invalid_target", "the selected gateway address is invalid")
			return
		case "not_found":
			writeWebError(w, http.StatusNotFound, "not_enrolled", "enroll a home network before reviewing a gateway check")
			return
		case "precondition_failed":
			writeWebError(w, http.StatusPreconditionFailed, "route_unavailable", "the current interface or route could not be verified")
			return
		}
	}
	writeWebError(w, http.StatusServiceUnavailable, "check_unavailable", "gateway check review is unavailable")
}

func writeGatewayRunError(w http.ResponseWriter, err error) {
	var response *localapi.ResponseError
	if errors.As(err, &response) {
		switch response.Code {
		case "cooldown", "busy":
			writeWebError(w, http.StatusConflict, response.Code, "another check or cooldown prevents this run; no approval was submitted")
			return
		case "precondition_failed", "review_expired":
			writeWebError(w, http.StatusPreconditionFailed, response.Code, "the reviewed check is no longer valid; no approval was submitted")
			return
		}
	}
	writeWebError(w, http.StatusServiceUnavailable, "check_unavailable", "gateway checks require the experimental controller opt-in; no approval was submitted")
}

func projectWebGatewayResult(result api.GatewayCheckResult, plan api.GatewayCheckPlan, now time.Time) (webGatewayRunResult, error) {
	if result.SchemaVersion != 1 || !gatewayReviewMatchesPlan(result.Review, plan) || !gatewayrun.ValidRunID(result.RunID) {
		return webGatewayRunResult{}, fmt.Errorf("invalid gateway result")
	}
	valid := false
	switch result.Outcome {
	case "completed":
		valid = result.FailureCode == "" && result.Measurement != nil && result.Measurement.Complete
	case "failed":
		valid = result.FailureCode == "execution_failed" && (result.Measurement == nil || !result.Measurement.Complete)
	case "canceled":
		valid = result.FailureCode == "canceled"
	case "indeterminate":
		valid = result.FailureCode == "execution_failed" && result.Measurement == nil
	case "blocked":
		valid = (result.FailureCode == "precondition_failed" || result.FailureCode == "review_expired") && result.Measurement == nil
	}
	if !valid {
		return webGatewayRunResult{}, fmt.Errorf("invalid gateway result")
	}
	out := webGatewayRunResult{Outcome: result.Outcome, RunID: result.RunID, FailureCode: result.FailureCode}
	if m := result.Measurement; m != nil {
		if gatewayrun.ValidateMeasurement(gatewayrun.Measurement(*m), now) != nil || m.StartedAt.Before(result.Review.CreatedAt) || !m.StartedAt.Before(result.Review.ExpiresAt) ||
			(m.CompletedAt != nil && !m.CompletedAt.Before(result.Review.ExpiresAt)) {
			return webGatewayRunResult{}, fmt.Errorf("invalid gateway measurement")
		}
		out.Measurement = &webGatewayMeasurement{StartedAt: m.StartedAt.UTC(), CompletedAt: m.CompletedAt,
			SendCalls: m.SendCalls, AcceptedRequests: m.AcceptedRequests, Replies: m.Replies,
			Timeouts: m.Timeouts, Complete: m.Complete, MeanRTTNS: m.MeanRTTNanoseconds}
	}
	return out, nil
}
