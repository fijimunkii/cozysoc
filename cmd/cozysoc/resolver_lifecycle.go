package main

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverroute"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverudp"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

// resolverRunLifecycle is owned by one controller API handler, not by web, a
// request, a destination, or Device Watch enablement. A future gated native consent adapter may expose it.
// Once closed, even an unsuccessful shutdown wait cannot replace its coordinator.
// All dependencies and method callers are compiled controller code.
type resolverRunLifecycle struct {
	mu      sync.Mutex
	control *resolverrun.Control
	closed  bool
}

func (l *resolverRunLifecycle) install(deps resolverrun.Dependencies) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.control != nil {
		return resolverrun.ErrUnavailable
	}
	if deps.Preflight == nil || deps.Executor == nil || deps.Auditor == nil {
		return resolverrun.ErrUnavailable
	}
	c, err := resolverrun.New(deps)
	if err != nil {
		return err
	}
	l.control = c
	return nil
}

func (l *resolverRunLifecycle) current() (*resolverrun.Control, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.control == nil {
		return nil, resolverrun.ErrUnavailable
	}
	return l.control, nil
}

// Internal entrypoints and a future native consent adapter share this owner.
// Read-only previews never call prepare or reserve a ticket.
func (l *resolverRunLifecycle) prepare(ctx context.Context, id string) (resolverrun.Review, error) {
	c, err := l.current()
	if err != nil {
		return resolverrun.Review{}, err
	}
	return c.Prepare(ctx, id)
}

func (l *resolverRunLifecycle) run(ctx context.Context, ticket resolverrun.Ticket, consent bool) (resolverrun.Result, error) {
	c, err := l.current()
	if err != nil {
		return resolverrun.Result{}, err
	}
	return c.Run(ctx, ticket, consent)
}

func (l *resolverRunLifecycle) shutdown(ctx context.Context) error {
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

// The controller owns this singleton even while execution endpoints are absent.
// Installation is inert, follows socket ownership, and never resolves settings.
func (h *controllerAPIHandler) startResolverRuns(auditor resolverrun.Auditor) error {
	if h == nil || h.store == nil || h.resolverRouteInspector == nil || h.now == nil || auditor == nil {
		return resolverrun.ErrUnavailable
	}
	if _, ok := h.store.(resolverConfigurationReader); !ok {
		return resolverrun.ErrUnavailable
	}
	return h.resolverRuns.install(resolverrun.Dependencies{Preflight: h.preflightResolverRun, Executor: resolverudp.NewCandidate(), Auditor: auditor, Now: h.now})
}

type resolverConfigurationReader interface {
	ActiveResolverConfiguration(context.Context, string) (storage.ResolverConfiguration, error)
}

func (h *controllerAPIHandler) resolverInputs(ctx context.Context, id string) (storage.ResolverConfiguration, resolverroute.Enrollment, error) {
	if h == nil || h.store == nil || ctx.Err() != nil {
		return storage.ResolverConfiguration{}, resolverroute.Enrollment{}, resolverrun.ErrPreflight
	}
	reader, ok := h.store.(resolverConfigurationReader)
	if !ok {
		return storage.ResolverConfiguration{}, resolverroute.Enrollment{}, resolverrun.ErrPreflight
	}
	settings, err := reader.ActiveResolverConfiguration(ctx, id)
	if err != nil {
		return storage.ResolverConfiguration{}, resolverroute.Enrollment{}, resolverrun.ErrPreflight
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil || binding.ScopeID != settings.ScopeID || ctx.Err() != nil {
		return storage.ResolverConfiguration{}, resolverroute.Enrollment{}, resolverrun.ErrPreflight
	}
	prefixes := make([]string, 0, len(binding.Prefixes))
	for _, raw := range binding.Prefixes {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return storage.ResolverConfiguration{}, resolverroute.Enrollment{}, resolverrun.ErrPreflight
		}
		// Passive enrollment includes link-local neighbor prefixes. The DNS route
		// profile compares the complete routable set and cannot send to link-local.
		if p.Addr().IsLinkLocalUnicast() {
			bits := 10
			if p.Addr().Is4() {
				bits = 16
			}
			if p.Bits() < bits {
				return storage.ResolverConfiguration{}, resolverroute.Enrollment{}, resolverrun.ErrPreflight
			}
			continue
		}
		prefixes = append(prefixes, raw)
	}
	slices.Sort(prefixes)
	return settings, resolverroute.Enrollment{Observer: nq.Observer{ScopeID: binding.ScopeID, SensorID: "controller-selected-resolver", InterfaceName: binding.InterfaceName, InterfaceIndex: binding.InterfaceIndex}, Prefixes: prefixes}, nil
}

func (h *controllerAPIHandler) preflightResolverRun(ctx context.Context, id string) (resolverrun.Selection, error) {
	selection, _, err := h.collectResolverPlan(ctx, id)
	return selection, err
}

// Each preflight re-reads immutable settings and durable enrollment around native
// route collection. Never reconstruct authority from a client preview or cache.
func (h *controllerAPIHandler) collectResolverPlan(ctx context.Context, id string) (resolverrun.Selection, storage.ResolverConfiguration, error) {
	fail := func() (resolverrun.Selection, storage.ResolverConfiguration, error) {
		return resolverrun.Selection{}, storage.ResolverConfiguration{}, resolverrun.ErrPreflight
	}
	if h == nil || h.now == nil || h.resolverRouteInspector == nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := h.now()
	settings, enrolled, err := h.resolverInputs(ctx, id)
	if err != nil {
		return fail()
	}
	selection, err := h.resolverRouteInspector.Inspect(ctx, enrolled, settings.Disclosure())
	if err != nil {
		return fail()
	}
	current, binding, err := h.resolverInputs(ctx, id)
	if err != nil || current.Disclosure() != settings.Disclosure() || current.ScopeID != settings.ScopeID || !current.CreatedAt.Equal(settings.CreatedAt) ||
		enrolled.Observer != binding.Observer || !slices.Equal(enrolled.Prefixes, binding.Prefixes) {
		return fail()
	}
	d := selection.Plan.Disclosure()
	expected, err := resolverplan.New(resolverplan.Binding{Observer: binding.Observer, Prefixes: binding.Prefixes, Source: d.Binding.Source}, current.Disclosure(), d.CreatedAt)
	now := h.now()
	if err != nil || !expected.SameSelection(selection.Plan) || !selection.Plan.Current(now) || now.Before(started) || now.Sub(started) >= 5*time.Second ||
		selection.RouteObservedAt.Before(started) || selection.RouteObservedAt.After(d.CreatedAt) || selection.RouteFreshUntil.After(selection.RouteObservedAt.Add(resolverplan.ReviewLifetime)) ||
		!now.Before(selection.RouteFreshUntil) || ctx.Err() != nil {
		return fail()
	}
	return selection, current, nil
}
