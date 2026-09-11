package localapi

import (
	"net/netip"
	"reflect"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func parseGatewayAddress(raw string) (netip.Addr, error) {
	if err := networkquality.ValidateGatewayPreviewTarget(raw); err != nil {
		return netip.Addr{}, err
	}
	return netip.ParseAddr(raw)
}

func wireGatewayBudget(b networkquality.GatewayProbeBudget) api.GatewayPlanBudget {
	return api.GatewayPlanBudget{MaxAttempts: b.MaxAttempts, MinIntervalMS: b.MinInterval.Milliseconds(),
		AttemptTimeoutMS: b.AttemptTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(),
		PayloadBytes: b.PayloadBytes, MaxICMPRequestBytes: b.MaxICMPRequestBytes,
		MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()}
}

func cloneGatewayReview(r api.GatewayCheckReview) api.GatewayCheckReview {
	r.Binding.Prefixes = slices.Clone(r.Binding.Prefixes)
	return r
}

func projectGatewayReview(r gatewayrun.Review, challenge string) api.GatewayCheckReview {
	s := r.Selection
	b := s.Plan.Binding
	return api.GatewayCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Challenge: challenge, Profile: gatewayrun.Profile,
		CreatedAt: s.Plan.CreatedAt.Round(0).UTC(), ExpiresAt: r.ExpiresAt.Round(0).UTC(),
		Binding: api.GatewayPlanBinding{ScopeID: b.ScopeID, InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex, Prefixes: slices.Clone(b.Prefixes)},
		Target:  s.Plan.Target.String(), Source: s.Source.String(), Budget: wireGatewayBudget(s.Plan.Budget)}
}

func gatewayTime(t time.Time) bool { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }

func validateGatewayReview(r api.GatewayCheckReview, target string, now time.Time) error {
	if r.SchemaVersion != 1 || r.Mode != "experimental-one-shot" || r.GatewayRoleVerified || r.Profile != gatewayrun.Profile || !gatewayChallenge.MatchString(r.Challenge) ||
		r.Target != target || r.Source == target || !gatewayTime(r.CreatedAt) || !gatewayTime(r.ExpiresAt) ||
		r.CreatedAt.After(now) || !r.ExpiresAt.After(now) || !r.ExpiresAt.After(r.CreatedAt) ||
		r.ExpiresAt.Sub(r.CreatedAt) > networkquality.GatewayReviewLifetime {
		return errGatewayProtocol
	}
	b := r.Binding
	binding := networkquality.GatewayPlanBinding{ScopeID: b.ScopeID, InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex, Prefixes: b.Prefixes}
	plan, err := networkquality.PreviewGatewayCheck(binding, target, r.CreatedAt)
	if err != nil || wireGatewayBudget(plan.Budget) != r.Budget || !slices.Equal(plan.Binding.Prefixes, b.Prefixes) {
		return errGatewayProtocol
	}
	if _, err := networkquality.PreviewGatewayCheck(binding, r.Source, r.CreatedAt); err != nil {
		return errGatewayProtocol
	}
	return nil
}

func projectGatewayResult(review api.GatewayCheckReview, result gatewayrun.Result, err error) api.GatewayCheckResult {
	out := api.GatewayCheckResult{SchemaVersion: 1, Review: cloneGatewayReview(review), RunID: result.RunID, Outcome: result.Outcome}
	if err != nil {
		out.FailureCode = gatewayErrorCode(err)
	}
	if s := result.Sample; s != nil {
		m := &api.GatewayRunMeasurement{StartedAt: s.StartedAt.Round(0).UTC(), SendCalls: s.SendCalls,
			AcceptedRequests: s.AcceptedRequests, Replies: s.Replies, Timeouts: s.Timeouts, Complete: s.Complete}
		if !s.CompletedAt.IsZero() {
			at := s.CompletedAt.Round(0).UTC()
			m.CompletedAt = &at
		}
		if s.MeanRTT != nil {
			ns := int64(*s.MeanRTT)
			m.MeanRTTNanoseconds = &ns
		}
		out.Measurement = m
	}
	return out
}

func validateGatewayResult(r api.GatewayCheckResult, review api.GatewayCheckReview, now time.Time) error {
	if r.SchemaVersion != 1 || !reflect.DeepEqual(r.Review, review) {
		return errGatewayProtocol
	}
	if r.Outcome == "declined" {
		if r.RunID != "" || r.Measurement != nil || r.FailureCode != "" {
			return errGatewayProtocol
		}
		return nil
	}
	if !gatewayChallenge.MatchString(r.RunID) {
		return errGatewayProtocol
	}
	if m := r.Measurement; m != nil {
		// Structural validation only. This does not reconstruct authority from a
		// DTO; approval remains the server's connection-local opaque ticket.
		if gatewayrun.ValidateMeasurement(gatewayrun.Measurement(*m), now) != nil || m.StartedAt.Before(review.CreatedAt) ||
			!m.StartedAt.Before(review.ExpiresAt) || (m.CompletedAt != nil && !m.CompletedAt.Before(review.ExpiresAt)) {
			return errGatewayProtocol
		}
	}
	switch r.Outcome {
	case "completed":
		if r.FailureCode == "" && r.Measurement != nil && r.Measurement.Complete {
			return nil
		}
	case "canceled":
		if r.FailureCode == "canceled" {
			return nil
		}
	case "failed":
		if r.FailureCode == "execution_failed" && (r.Measurement == nil || !r.Measurement.Complete) {
			return nil
		}
	case "indeterminate":
		if r.FailureCode == "execution_failed" && r.Measurement == nil {
			return nil
		}
	case "blocked":
		if (r.FailureCode == "precondition_failed" || r.FailureCode == "review_expired") && r.Measurement == nil {
			return nil
		}
	}
	return errGatewayProtocol
}
