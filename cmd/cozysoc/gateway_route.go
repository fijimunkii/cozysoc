package main

import (
	"context"
	"errors"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func (h *controllerAPIHandler) reviewGatewayRoute(ctx context.Context, plan networkquality.GatewayCheckPlan) (api.GatewayRouteReview, error) {
	unavailable := api.GatewayRouteReview{State: "unavailable", Reason: "source-unavailable"}
	evidence, err := h.collectGatewayRouteEvidence(ctx, plan)
	if ctx.Err() != nil {
		return api.GatewayRouteReview{}, ctx.Err()
	}
	if err != nil {
		switch {
		case errors.Is(err, gatewayroute.ErrUnsupported):
			return api.GatewayRouteReview{State: "unsupported", Reason: "platform-unsupported"}, nil
		case errors.Is(err, gatewayroute.ErrMismatch):
			return api.GatewayRouteReview{State: "mismatch", Reason: "route-or-source-mismatch"}, nil
		case errors.Is(err, gatewayroute.ErrPermission):
			return api.GatewayRouteReview{State: "unavailable", Reason: "permission-required"}, nil
		case errors.Is(err, gatewayroute.ErrNoRoute):
			return api.GatewayRouteReview{State: "unavailable", Reason: "no-route"}, nil
		default:
			return unavailable, nil
		}
	}
	observedAt, freshUntil := evidence.ObservedAt.UTC(), evidence.FreshUntil.UTC()
	return api.GatewayRouteReview{State: "consistent", Source: "darwin-rtm-get", SourceAddress: evidence.SourceAddress.String(),
		ObservedAt: &observedAt, FreshUntil: &freshUntil, SendBindingVerified: false}, nil
}

// Shared trusted evidence path. Preview projection is never an execution input.
func (h *controllerAPIHandler) collectGatewayRouteEvidence(ctx context.Context, plan networkquality.GatewayCheckPlan) (gatewayroute.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return gatewayroute.Evidence{}, err
	}
	if h.gatewayRouteInspector == nil {
		return gatewayroute.Evidence{}, gatewayroute.ErrUnavailable
	}
	evidence, err := h.gatewayRouteInspector.Inspect(ctx, plan.Binding, plan.Target)
	if ctx.Err() != nil {
		return gatewayroute.Evidence{}, ctx.Err()
	}
	if err != nil {
		return gatewayroute.Evidence{}, err
	}
	now := h.now().UTC()
	if evidence.InterfaceName != plan.Binding.InterfaceName || evidence.InterfaceIndex != plan.Binding.InterfaceIndex ||
		evidence.SourceAddress == plan.Target || evidence.ObservedAt.IsZero() || evidence.ObservedAt.Before(plan.CreatedAt) || evidence.ObservedAt.After(now) ||
		!evidence.FreshUntil.Equal(evidence.ObservedAt.Add(networkquality.GatewayReviewLifetime)) || !evidence.FreshUntil.After(now) ||
		now.Sub(plan.CreatedAt) > 5*time.Second {
		return gatewayroute.Evidence{}, gatewayroute.ErrUnavailable
	}
	if _, err := networkquality.PreviewGatewayCheck(plan.Binding, evidence.SourceAddress.String(), now); err != nil {
		return gatewayroute.Evidence{}, gatewayroute.ErrUnavailable
	}
	return evidence, nil
}
