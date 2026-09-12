package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverroute"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type resolverInspectorFunc func(context.Context, resolverroute.Enrollment, resolverplan.Configuration) (resolverrun.Selection, error)

func (f resolverInspectorFunc) Inspect(ctx context.Context, e resolverroute.Enrollment, c resolverplan.Configuration) (resolverrun.Selection, error) {
	return f(ctx, e, c)
}

type resolverExecutorFunc func(context.Context, resolverrun.Request) (nq.ResolverMeasurement, error)

func (f resolverExecutorFunc) ExecuteResolver(ctx context.Context, r resolverrun.Request) (nq.ResolverMeasurement, error) {
	return f(ctx, r)
}
func resolverParams() api.ResolverSettingsParams {
	return api.ResolverSettingsParams{Endpoint: "192.0.2.53:53", Name: "private.example.", Family: "ipv4", Transport: "udp", QueryType: "A", Expect: "answer", DestinationScope: "enrolled-prefix"}
}
func resolverHandler(t *testing.T) (*controllerAPIHandler, *storage.Store, *int) {
	t.Helper()
	s, err := storage.Open(t.TempDir(), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	meta, err := devicewatch.EncodeScopeMetadata(devicewatch.ScopeBinding{InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.0.2.0/24", "fe80::/64"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnrollDeviceWatchScope(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Round(0).UTC()
	h := &controllerAPIHandler{store: s, now: func() time.Time { return now }}
	calls := new(int)
	h.resolverRouteInspector = resolverInspectorFunc(func(ctx context.Context, e resolverroute.Enrollment, c resolverplan.Configuration) (resolverrun.Selection, error) {
		*calls++
		p, err := resolverplan.New(resolverplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: netip.MustParseAddr("192.0.2.10")}, c, h.now())
		return resolverrun.Selection{Plan: p, RouteObservedAt: h.now(), RouteFreshUntil: h.now().Add(30 * time.Second)}, err
	})
	t.Cleanup(func() { _ = h.resolverRuns.shutdown(context.Background()) })
	return h, s, calls
}
func saveResolver(t *testing.T, h *controllerAPIHandler) string {
	t.Helper()
	result, err := h.SaveResolver(context.Background(), resolverParams())
	if err != nil || len(result.Items) != 1 {
		t.Fatal("save", err)
	}
	return result.Items[0].SelectionID
}

func TestResolverSettingsPreviewAndLifecycleStayInert(t *testing.T) {
	ctx := context.Background()
	h, s, calls := resolverHandler(t)
	if err := h.startResolverRuns(s); err != nil {
		t.Fatal(err)
	}
	first, _ := h.resolverRuns.current()
	if err := h.startResolverRuns(s); err != resolverrun.ErrUnavailable {
		t.Fatal("second coordinator installed", err)
	}
	id := saveResolver(t, h)
	listed, err := h.ListResolvers(ctx)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].SelectionID != id || *calls != 0 || listed.ConsentGranted {
		t.Fatal("settings collected or granted consent", err)
	}
	preview, err := h.PreviewResolver(ctx, api.ResolverIDParams{SelectionID: id})
	if err != nil || preview.Mode != "preview-only" || preview.ExecutionAvailable || preview.ConsentGranted || !preview.MayForwardUpstream || preview.OutsideEnrolledPrefixes || preview.Budget.MaxSendCalls != 1 || len(preview.Binding.Prefixes) != 1 || *calls != 1 {
		t.Fatalf("preview failed: %+v %v", preview, err)
	}
	// Preview must not reserve a run ticket or reset the singleton quiet interval.
	if _, err := h.resolverRuns.prepare(ctx, id); err != resolverrun.ErrCooldown {
		t.Fatal("startup quiet interval lost", err)
	}
	current, _ := h.resolverRuns.current()
	if current != first {
		t.Fatal("preview replaced owner")
	}
	if _, err := h.RetireResolver(ctx, api.ResolverIDParams{SelectionID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PreviewResolver(ctx, api.ResolverIDParams{SelectionID: id}); err == nil || *calls != 1 {
		t.Fatal("retired config reached route")
	}
	if err := h.resolverRuns.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.startResolverRuns(s); err != resolverrun.ErrUnavailable {
		t.Fatal("closed owner reinstalled")
	}
}

type resolverScopeOverride struct {
	*storage.Store
	scopes []domain.NetworkScope
	fail   bool
}

func (s *resolverScopeOverride) ListActiveDeviceWatchScopes(context.Context) ([]domain.NetworkScope, error) {
	if s.fail {
		return nil, errors.New("private storage failure")
	}
	return s.scopes, nil
}

func TestResolverPreflightRechecksSettingsAndScopeAroundRoute(t *testing.T) {
	for _, mode := range []string{"retired", "revoked", "new-scope", "prefix-change", "storage-error", "canceled", "stale-route", "wrong-selection"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h, s, _ := resolverHandler(t)
			id := saveResolver(t, h)
			scopes, err := s.ListActiveDeviceWatchScopes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			store := &resolverScopeOverride{Store: s, scopes: scopes}
			h.store = store
			original := h.resolverRouteInspector
			h.resolverRouteInspector = resolverInspectorFunc(func(ctx context.Context, e resolverroute.Enrollment, c resolverplan.Configuration) (resolverrun.Selection, error) {
				selection, err := original.Inspect(ctx, e, c)
				switch mode {
				case "retired":
					if err := s.RetireResolverConfiguration(ctx, id); err != nil {
						t.Fatal(err)
					}
				case "revoked":
					store.scopes = nil
				case "new-scope":
					store.scopes[0].ID = "scope.replacement"
				case "prefix-change":
					store.scopes[0].Metadata = json.RawMessage(`{"device_watch":{"interface_name":"fixture0","interface_index":7,"prefixes":["192.0.3.0/24"]}}`)
				case "storage-error":
					store.fail = true
				case "canceled":
					cancel()
				case "stale-route":
					selection.RouteObservedAt = h.now().Add(-time.Second)
				case "wrong-selection":
					c.Name = "other.example."
					selection.Plan, _ = resolverplan.New(resolverplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: netip.MustParseAddr("192.0.2.10")}, c, h.now())
				}
				return selection, err
			})
			if selection, err := h.preflightResolverRun(ctx, id); err == nil || selection.Plan.Current(h.now()) {
				t.Fatal("changed/invalid preflight accepted")
			}
		})
	}
}

func TestResolverRetirementBetweenReviewAndRunBlocksExecution(t *testing.T) {
	ctx := context.Background()
	h, s, _ := resolverHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	id := saveResolver(t, h)
	calls := 0
	if err := h.resolverRuns.install(resolverrun.Dependencies{Now: h.now, Auditor: s, Preflight: h.preflightResolverRun, Executor: resolverExecutorFunc(func(context.Context, resolverrun.Request) (nq.ResolverMeasurement, error) {
		calls++
		return nq.ResolverMeasurement{}, errors.New("unexpected execution")
	})}); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	review, err := h.resolverRuns.prepare(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetireResolverConfiguration(ctx, id); err != nil {
		t.Fatal(err)
	}
	result, err := h.resolverRuns.run(ctx, review.Ticket, true)
	if err != resolverrun.ErrPreflight || result.Outcome != "blocked" || calls != 0 {
		t.Fatal("retired review executed", err)
	}
}

func TestResolverShutdownJoinsBeforeStorageMayClose(t *testing.T) {
	ctx := context.Background()
	h, s, _ := resolverHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	id := saveResolver(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	deps := resolverrun.Dependencies{Now: h.now, Auditor: s, Preflight: h.preflightResolverRun, Executor: resolverExecutorFunc(func(ctx context.Context, _ resolverrun.Request) (nq.ResolverMeasurement, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nq.ResolverMeasurement{}, ctx.Err()
	})}
	if err := h.resolverRuns.install(deps); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	r, err := h.resolverRuns.prepare(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := h.resolverRuns.run(ctx, r.Ticket, true); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not start")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := h.resolverRuns.shutdown(canceled); err != context.Canceled {
		t.Fatal("unfinished run reported drained", err)
	}
	if err := h.resolverRuns.install(deps); err != resolverrun.ErrUnavailable {
		t.Fatal("draining owner replaced")
	}
	if _, err := h.resolverRuns.prepare(ctx, id); err != resolverrun.ErrUnavailable {
		t.Fatal("draining owner admitted")
	}
	close(release)
	if err := h.resolverRuns.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
}

func TestResolverCommandRejectsDefaultsAndAuthorityFlags(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for _, args := range [][]string{{"resolver-save"}, {"resolver-save", "--yes"}, {"resolver-save", "--name", "example."}, {"resolver-plan", "192.0.2.53"}, {"resolver-retire", ""}, {"resolver-list", "--consent"}, {"resolver-list", "extra"}} {
		if err := run(context.Background(), args, out, out); err == nil {
			t.Fatal("invalid command accepted", strings.Join(args, " "))
		}
	}
	for _, command := range []string{"resolver-save", "resolver-list", "resolver-retire", "resolver-plan"} {
		if err := run(context.Background(), []string{command, "--help"}, out, out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolverCLIWorkflow(t *testing.T) {
	h, _, calls := resolverHandler(t)
	dir := t.TempDir()
	server, err := localapi.NewServer(dir, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx) }()
	defer func() { cancel(); _ = server.Close(); <-done }()
	call := func(command string, args ...string) json.RawMessage {
		t.Helper()
		file, err := os.CreateTemp(t.TempDir(), "output")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		full := append([]string{command, "--state-dir", dir}, args...)
		if err := run(ctx, full, file, file); err != nil {
			t.Fatal(command, err)
		}
		raw, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var saved api.ResolverSettingsResult
	raw := call("resolver-save", "--endpoint", "192.0.2.53:53", "--name", "private.example.", "--family", "ipv4", "--transport", "udp", "--query-type", "A", "--expect", "answer", "--destination-scope", "enrolled-prefix")
	if json.Unmarshal(raw, &saved) != nil || len(saved.Items) != 1 || saved.ConsentGranted || *calls != 0 {
		t.Fatal("save failed or touched route")
	}
	id := saved.Items[0].SelectionID
	var listed api.ResolverSettingsResult
	if json.Unmarshal(call("resolver-list"), &listed) != nil || len(listed.Items) != 1 || listed.Items[0] != saved.Items[0] {
		t.Fatal("list changed saved settings")
	}
	var plan api.ResolverPlan
	if json.Unmarshal(call("resolver-plan", id), &plan) != nil || plan.Configuration.SelectionID != id || plan.Mode != "preview-only" || plan.ExecutionAvailable || plan.ConsentGranted || *calls != 1 {
		t.Fatal("preview failed")
	}
	var retired api.ResolverRetireResult
	if json.Unmarshal(call("resolver-retire", id), &retired) != nil || retired.State != "retired" {
		t.Fatal("retirement failed")
	}
	if json.Unmarshal(call("resolver-list"), &listed) != nil || len(listed.Items) != 0 || *calls != 1 {
		t.Fatal("retired settings still selected")
	}
}
