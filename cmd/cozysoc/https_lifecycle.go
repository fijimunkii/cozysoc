package main

import (
	"context"
	"sync"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	"github.com/fijimunkii/cozysoc/internal/controller/httpstcp"
)

// httpsRunLifecycle is owned by one controller API handler, not by web, a
// request, a destination, or Device Watch enablement. No native consent or browser execution adapter exposes it yet.
// Once closed, even an unsuccessful shutdown wait cannot replace its coordinator.
// All dependencies and method callers are compiled controller code.
type httpsRunLifecycle struct {
	mu      sync.Mutex
	control *httpsrun.Control
	closed  bool
}

func (l *httpsRunLifecycle) install(deps httpsrun.Dependencies) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.control != nil {
		return httpsrun.ErrUnavailable
	}
	if deps.Preflight == nil || deps.Executor == nil || deps.Auditor == nil {
		return httpsrun.ErrUnavailable
	}
	c, err := httpsrun.New(deps)
	if err != nil {
		return err
	}
	l.control = c
	return nil
}

func (l *httpsRunLifecycle) current() (*httpsrun.Control, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.control == nil {
		return nil, httpsrun.ErrUnavailable
	}
	return l.control, nil
}

// Internal entrypoints share this owner; none are exposed over the native API.
// Read-only previews never call prepare or reserve a ticket.
func (l *httpsRunLifecycle) prepare(ctx context.Context, id string) (httpsrun.Review, error) {
	c, err := l.current()
	if err != nil {
		return httpsrun.Review{}, err
	}
	return c.Prepare(ctx, id)
}

func (l *httpsRunLifecycle) run(ctx context.Context, ticket httpsrun.Ticket, consent bool) (httpsrun.Result, error) {
	c, err := l.current()
	if err != nil {
		return httpsrun.Result{}, err
	}
	return c.Run(ctx, ticket, consent)
}

func (l *httpsRunLifecycle) shutdown(ctx context.Context) error {
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

// The controller owns this singleton even while native execution is disabled.
// Installation is inert, follows socket ownership, and never resolves settings.
func (h *controllerAPIHandler) startHTTPSRuns(auditor httpsrun.Auditor) error {
	if h == nil || h.store == nil || h.httpsRouteInspector == nil || h.now == nil || auditor == nil {
		return httpsrun.ErrUnavailable
	}
	if _, ok := h.store.(httpsConfigurationReader); !ok {
		return httpsrun.ErrUnavailable
	}
	return h.httpsRuns.install(httpsrun.Dependencies{Preflight: h.preflightHTTPSRun, Executor: httpstcp.NewCandidate(), Auditor: auditor, Now: h.now})
}

func (h *controllerAPIHandler) preflightHTTPSRun(ctx context.Context, id string) (httpsrun.Selection, error) {
	selection, _, err := h.collectHTTPSPlan(ctx, id)
	if err != nil {
		return httpsrun.Selection{}, httpsrun.ErrPreflight
	}
	return selection, nil
}

// HTTPSCheckControl exposes the installed owner only for the native opt-in.
func (h *controllerAPIHandler) HTTPSCheckControl() (*httpsrun.Control, error) {
	if h == nil || !h.httpsChecksEnabled {
		return nil, httpsrun.ErrUnavailable
	}
	return h.httpsRuns.current()
}
