package main

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type lifecycleAuditor struct {
	mu     sync.Mutex
	events []gatewayrun.Event
}

func (a *lifecycleAuditor) InsertGatewayRunAudit(_ context.Context, e gatewayrun.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := gatewayrun.ValidateEvent(e); err != nil {
		return err
	}
	a.events = append(a.events, e)
	return nil
}

func (a *lifecycleAuditor) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events)
}

type lifecycleExecutor func(context.Context, gatewayrun.Selection) (gatewayicmp.Sample, error)

func (f lifecycleExecutor) ExecuteGateway(ctx context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
	return f(ctx, s)
}

func lifecycleHandler(t *testing.T) (*controllerAPIHandler, *fakeDeviceStore, *qualityInspector) {
	t.Helper()
	h, store, inspector := qualityFixture(t)
	h.gatewayRouteInspector = routeInspectorFunc(func(_ context.Context, b networkquality.GatewayPlanBinding, _ netip.Addr) (gatewayroute.Evidence, error) {
		return gatewayroute.Evidence{InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex,
			SourceAddress: netip.MustParseAddr("192.168.50.23"), ObservedAt: h.now(), FreshUntil: h.now().Add(30 * time.Second)}, nil
	})
	t.Cleanup(func() {
		if err := h.gatewayRuns.shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return h, store, inspector
}

func TestGatewayControllerInstallationAndPreviewDoNotGrantAuthority(t *testing.T) {
	h, store, inspector := lifecycleHandler(t)
	at := h.now()
	var clockMu sync.Mutex
	h.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return at }
	audit := &lifecycleAuditor{}
	if err := h.startGatewayRuns(audit); err != nil {
		t.Fatal(err)
	}
	first, err := h.gatewayRuns.current()
	if err != nil || first == nil || inspector.calls != 0 || audit.count() != 0 {
		t.Fatal("installation collected or executed", err)
	}
	if err := h.startGatewayRuns(audit); err != gatewayrun.ErrUnavailable {
		t.Fatal("second installation replaced coordinator", err)
	}
	second, _ := h.gatewayRuns.current()
	if second != first {
		t.Fatal("controller ownership was replaced")
	}
	target := netip.MustParseAddr("192.168.50.1")
	if _, err := h.gatewayRuns.prepare(context.Background(), target); err != gatewayrun.ErrCooldown {
		t.Fatal("new owner skipped startup quiet interval", err)
	}
	for i := 0; i < 2; i++ {
		plan, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: target.String()})
		if err != nil || plan.Mode != "preview-only" || plan.ConsentGranted || plan.ExecutionAvailable {
			t.Fatalf("preview acquired execution authority: %+v %v", plan, err)
		}
	}
	clockMu.Lock()
	at = at.Add(gatewayrun.RunInterval)
	clockMu.Unlock()
	review, err := h.gatewayRuns.prepare(context.Background(), target)
	if err != nil || audit.count() != 0 {
		t.Fatal("preview reserved a ticket or recorded consent", err)
	}
	if _, err := h.gatewayRuns.run(context.Background(), review.Ticket, false); err != gatewayrun.ErrConsent {
		t.Fatal("internal run lost explicit consent boundary", err)
	}
	// Stop without ever invoking the concrete sender. Enrollment is untouched.
	if err := h.gatewayRuns.shutdown(context.Background()); err != nil || audit.count() != 0 || store.enrollMetadata != nil {
		t.Fatal("unused ownership shutdown created side effects", err)
	}
	if err := h.startGatewayRuns(audit); err != gatewayrun.ErrUnavailable {
		t.Fatal("shutdown allowed fresh cooldown/approval instance", err)
	}
	if _, err := h.gatewayRuns.run(context.Background(), review.Ticket, true); err != gatewayrun.ErrUnavailable {
		t.Fatal("closed owner admitted saved ticket", err)
	}
}

func TestGatewayLifecycleMissingDependenciesAndCloseBeforeInstall(t *testing.T) {
	h, _, _ := lifecycleHandler(t)
	if err := h.startGatewayRuns(nil); err != gatewayrun.ErrUnavailable {
		t.Fatal("missing auditor accepted", err)
	}
	if err := h.gatewayRuns.install(gatewayrun.Dependencies{}); err != gatewayrun.ErrUnavailable {
		t.Fatal("missing collaborators accepted", err)
	}
	if _, err := h.gatewayRuns.prepare(context.Background(), netip.MustParseAddr("192.168.50.1")); err != gatewayrun.ErrUnavailable {
		t.Fatal("uninitialized owner admitted work", err)
	}
	if err := h.gatewayRuns.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.startGatewayRuns(&lifecycleAuditor{}); err != gatewayrun.ErrUnavailable {
		t.Fatal("late initialization after teardown", err)
	}
}

func TestGatewayPreflightRechecksDurableScopeAfterRoute(t *testing.T) {
	for _, mode := range []string{"revoked", "new-scope", "storage-error", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h, store, _ := lifecycleHandler(t)
			original := h.gatewayRouteInspector
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.gatewayRouteInspector = routeInspectorFunc(func(ctx context.Context, b networkquality.GatewayPlanBinding, target netip.Addr) (gatewayroute.Evidence, error) {
				e, err := original.Inspect(ctx, b, target)
				switch mode {
				case "revoked":
					store.activeScopes = nil
				case "new-scope":
					store.activeScopes[0].ID = "scope.replaced"
				case "storage-error":
					store.activeErr = errors.New("storage unavailable")
				case "canceled":
					cancel()
				}
				return e, err
			})
			selection, err := h.preflightGatewayRun(ctx, netip.MustParseAddr("192.168.50.1"))
			if err == nil || selection.Plan.Target.IsValid() {
				t.Fatal("scope changed during collection but preflight succeeded")
			}
		})
	}
}

func TestGatewayRunRevalidationUsesCurrentStorageNotReviewedDTO(t *testing.T) {
	h, store, _ := lifecycleHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	audit := &lifecycleAuditor{}
	calls := 0
	if err := h.gatewayRuns.install(gatewayrun.Dependencies{Now: h.now, Auditor: audit, Preflight: h.preflightGatewayRun,
		Executor: lifecycleExecutor(func(context.Context, gatewayrun.Selection) (gatewayicmp.Sample, error) {
			calls++
			return gatewayicmp.Sample{}, errors.New("must never reach executor")
		})}); err != nil {
		t.Fatal(err)
	}
	at = at.Add(gatewayrun.RunInterval)
	r, err := h.gatewayRuns.prepare(context.Background(), netip.MustParseAddr("192.168.50.1"))
	if err != nil {
		t.Fatal(err)
	}
	store.activeScopes = nil
	got, err := h.gatewayRuns.run(context.Background(), r.Ticket, true)
	if err != gatewayrun.ErrPreflight || got.Outcome != "blocked" || got.Sample != nil || calls != 0 || audit.count() != 2 {
		t.Fatalf("revoked scope was used: %+v %v", got, err)
	}
}

func TestGatewayOwnerShutdownWaitsBeforeStorageDependencyMayClose(t *testing.T) {
	h, _, _ := lifecycleHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	audit := &lifecycleAuditor{}
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	deps := gatewayrun.Dependencies{Now: h.now, Auditor: audit, Preflight: h.preflightGatewayRun,
		Executor: lifecycleExecutor(func(ctx context.Context, _ gatewayrun.Selection) (gatewayicmp.Sample, error) {
			close(entered)
			<-ctx.Done()
			<-release // Simulate a collaborator still cleaning up after cancellation.
			return gatewayicmp.Sample{}, ctx.Err()
		})}
	if err := h.gatewayRuns.install(deps); err != nil {
		t.Fatal(err)
	}
	at = at.Add(gatewayrun.RunInterval)
	r, err := h.gatewayRuns.prepare(context.Background(), netip.MustParseAddr("192.168.50.1"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := h.gatewayRuns.run(context.Background(), r.Ticket, true); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.gatewayRuns.shutdown(ctx); err != context.Canceled {
		t.Fatal("unfinished work reported drained", err)
	}
	if err := h.gatewayRuns.install(deps); err != gatewayrun.ErrUnavailable {
		t.Fatal("drain timeout replaced ownership", err)
	}
	if _, err := h.gatewayRuns.prepare(context.Background(), r.Selection.Plan.Target); err != gatewayrun.ErrUnavailable {
		t.Fatal("drain timeout reopened admission", err)
	}
	close(release)
	if err := h.gatewayRuns.shutdown(context.Background()); err != nil || audit.count() != 3 {
		t.Fatal("storage may close before canceled terminal audit", err)
	}
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
}

func TestGatewayPreflightComparesCanonicalScopeNotStoredPrefixOrder(t *testing.T) {
	h, store, _ := lifecycleHandler(t)
	binding, err := devicewatch.ParseScopeBinding(store.activeScopes[0])
	if err != nil {
		t.Fatal(err)
	}
	binding.Prefixes[0], binding.Prefixes[1] = binding.Prefixes[1], binding.Prefixes[0]
	store.activeScopes[0].Metadata, err = devicewatch.EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	s, err := h.preflightGatewayRun(context.Background(), netip.MustParseAddr("192.168.50.1"))
	if err != nil || s.Source.String() != "192.168.50.23" || s.Plan.Binding.Prefixes[0] != "192.168.50.0/24" {
		t.Fatalf("equivalent scope became changed: %+v %v", s, err)
	}
}
