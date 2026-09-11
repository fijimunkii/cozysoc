package main

import (
	"context"
	"net/netip"
	"slices"
	"sync"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// gatewayRunLifecycle is owned by one controller API handler, not by web, a
// request, a destination, or Device Watch enablement. Only the gated native consent adapter may expose it.
// Once closed, even an unsuccessful shutdown wait cannot replace its coordinator.
// All dependencies and method callers are compiled controller code.
type gatewayRunLifecycle struct {
	mu      sync.Mutex
	control *gatewayrun.Control
	closed  bool
}

func (l *gatewayRunLifecycle) install(deps gatewayrun.Dependencies) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.control != nil {
		return gatewayrun.ErrUnavailable
	}
	if deps.Preflight == nil || deps.Executor == nil || deps.Auditor == nil {
		return gatewayrun.ErrUnavailable
	}
	c, err := gatewayrun.New(deps)
	if err != nil {
		return err
	}
	l.control = c
	return nil
}

func (l *gatewayRunLifecycle) current() (*gatewayrun.Control, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.control == nil {
		return nil, gatewayrun.ErrUnavailable
	}
	return l.control, nil
}

// Internal entrypoints and the gated native consent adapter share this owner.
// Read-only previews never call prepare or reserve a ticket.
func (l *gatewayRunLifecycle) prepare(ctx context.Context, target netip.Addr) (gatewayrun.Review, error) {
	c, err := l.current()
	if err != nil {
		return gatewayrun.Review{}, err
	}
	return c.Prepare(ctx, target)
}

func (l *gatewayRunLifecycle) run(ctx context.Context, ticket gatewayrun.Ticket, consent bool) (gatewayrun.Result, error) {
	c, err := l.current()
	if err != nil {
		return gatewayrun.Result{}, err
	}
	return c.Run(ctx, ticket, consent)
}

func (l *gatewayRunLifecycle) shutdown(ctx context.Context) error {
	l.mu.Lock()
	l.closed = true
	c := l.control
	if c != nil {
		c.Close() // Close admission before releasing ownership's mutex.
	}
	l.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Shutdown(ctx)
}

func (h *controllerAPIHandler) startGatewayRuns(auditor gatewayrun.Auditor) error {
	if h == nil || h.store == nil || h.networkInspector == nil || h.gatewayRouteInspector == nil || h.now == nil || auditor == nil {
		return gatewayrun.ErrUnavailable
	}
	return h.gatewayRuns.install(gatewayrun.Dependencies{
		Preflight: h.preflightGatewayRun,
		Executor:  gatewayrun.NewICMPExecutor(gatewayicmp.NewCandidate()),
		Auditor:   auditor,
		Now:       h.now,
	})
}

// Resolve enrolled scope from storage on EVERY prepare/revalidation. Use native
// plan and route evidence directly, never convert the public preview into a grant.
func (h *controllerAPIHandler) preflightGatewayRun(ctx context.Context, target netip.Addr) (gatewayrun.Selection, error) {
	plan, _, err := h.collectGatewayCheckPlan(ctx, api.GatewayPlanParams{Target: target.String()})
	if err != nil {
		return gatewayrun.Selection{}, err
	}
	e, err := h.collectGatewayRouteEvidence(ctx, plan)
	if err != nil {
		return gatewayrun.Selection{}, err
	}
	// Route lookup may overlap an enrollment change. Require the same durable
	// scope after collection; matching OS prefixes alone cannot retain authority.
	current, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return gatewayrun.Selection{}, err
	}
	canonical, err := networkquality.PreviewGatewayCheck(current, target.String(), plan.CreatedAt)
	if err != nil {
		return gatewayrun.Selection{}, gatewayrun.ErrPreflight
	}
	current = canonical.Binding
	b := plan.Binding
	if current.ScopeID != b.ScopeID || current.InterfaceName != b.InterfaceName || current.InterfaceIndex != b.InterfaceIndex || !slices.Equal(current.Prefixes, b.Prefixes) {
		return gatewayrun.Selection{}, gatewayrun.ErrPreflight
	}
	return gatewayrun.Selection{Plan: plan, Source: e.SourceAddress,
		RouteObservedAt: e.ObservedAt, RouteFreshUntil: e.FreshUntil}, nil
}

// GatewayCheckControl is the sole native protocol adapter to this owner's
// coordinator. Ordinary serve/dev/web startup does not enable active checks.
func (h *controllerAPIHandler) GatewayCheckControl() (*gatewayrun.Control, error) {
	if h == nil || !h.gatewayChecksEnabled {
		return nil, gatewayrun.ErrUnavailable
	}
	return h.gatewayRuns.current()
}
