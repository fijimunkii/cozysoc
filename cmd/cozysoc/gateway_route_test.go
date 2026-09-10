package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type routeInspectorFunc func(context.Context, networkquality.GatewayPlanBinding, netip.Addr) (gatewayroute.Evidence, error)

func (f routeInspectorFunc) Inspect(ctx context.Context, b networkquality.GatewayPlanBinding, target netip.Addr) (gatewayroute.Evidence, error) {
	return f(ctx, b, target)
}

func TestGatewayPreviewIncludesRouteEvidenceWithoutGrant(t *testing.T) {
	h, store, osInspector := qualityFixture(t)
	calls := 0
	h.gatewayRouteInspector = routeInspectorFunc(func(ctx context.Context, b networkquality.GatewayPlanBinding, target netip.Addr) (gatewayroute.Evidence, error) {
		calls++
		if b.ScopeID != "scope.home" || b.InterfaceName != "en0" || b.InterfaceIndex != 7 || target.String() != "192.168.50.1" {
			t.Fatal("route selection escaped controller context")
		}
		return gatewayroute.Evidence{InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex, SourceAddress: netip.MustParseAddr("192.168.50.23"), ObservedAt: h.now(), FreshUntil: h.now().Add(30 * time.Second)}, nil
	})
	plan, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
	if err != nil || plan.Route.State != "consistent" || plan.Route.SourceAddress != "192.168.50.23" || plan.Route.Source != "darwin-rtm-get" || plan.Route.ObservedAt == nil || plan.Route.FreshUntil == nil {
		t.Fatalf("route projection: %+v %v", plan, err)
	}
	if plan.ExecutionAvailable || plan.ConsentGranted || plan.Target.RoleVerified || plan.Route.SendBindingVerified || plan.Mode != "preview-only" || calls != 1 || osInspector.calls != 1 || store.enrollMetadata != nil {
		t.Fatal("metadata became send authority or storage mutation")
	}
	if len(plan.Limitations) != 7 || !strings.Contains(strings.Join(plan.Limitations, " "), "does not prove") {
		t.Fatal("route interpretation limits missing")
	}
}

func TestGatewayPreviewRouteFailuresKeepAuthorityFalseAndErrorsBounded(t *testing.T) {
	for _, tc := range []struct {
		err           error
		state, reason string
	}{
		{gatewayroute.ErrUnsupported, "unsupported", "platform-unsupported"}, {gatewayroute.ErrMismatch, "mismatch", "route-or-source-mismatch"},
		{gatewayroute.ErrPermission, "unavailable", "permission-required"}, {gatewayroute.ErrNoRoute, "unavailable", "no-route"},
		{errors.New("private-route-dump"), "unavailable", "source-unavailable"}, {context.DeadlineExceeded, "unavailable", "source-unavailable"},
	} {
		h, _, _ := qualityFixture(t)
		h.gatewayRouteInspector = routeInspectorFunc(func(context.Context, networkquality.GatewayPlanBinding, netip.Addr) (gatewayroute.Evidence, error) {
			return gatewayroute.Evidence{}, tc.err
		})
		plan, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
		if err != nil || plan.Route.State != tc.state || plan.Route.Reason != tc.reason || plan.Route.SourceAddress != "" || plan.Route.ObservedAt != nil || plan.Route.FreshUntil != nil || plan.Route.SendBindingVerified || plan.ExecutionAvailable || plan.ConsentGranted {
			t.Fatalf("%+v %v", plan, err)
		}
		raw, _ := json.Marshal(plan)
		if strings.Contains(string(raw), "private-route-dump") {
			t.Fatal("diagnostics leaked")
		}
	}
}

func TestGatewayPreviewRejectsInvalidRouteProjection(t *testing.T) {
	for _, edit := range []func(*gatewayroute.Evidence){
		func(e *gatewayroute.Evidence) { e.InterfaceName = "utun0" }, func(e *gatewayroute.Evidence) { e.InterfaceIndex++ },
		func(e *gatewayroute.Evidence) { e.SourceAddress = netip.MustParseAddr("8.8.8.8") }, func(e *gatewayroute.Evidence) { e.SourceAddress = netip.MustParseAddr("192.168.50.1") },
		func(e *gatewayroute.Evidence) { e.ObservedAt = e.ObservedAt.Add(-time.Minute) }, func(e *gatewayroute.Evidence) { e.ObservedAt = e.ObservedAt.Add(time.Second) },
		func(e *gatewayroute.Evidence) { e.FreshUntil = e.FreshUntil.Add(time.Hour) },
	} {
		h, _, _ := qualityFixture(t)
		h.gatewayRouteInspector = routeInspectorFunc(func(_ context.Context, b networkquality.GatewayPlanBinding, _ netip.Addr) (gatewayroute.Evidence, error) {
			e := gatewayroute.Evidence{InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex, SourceAddress: netip.MustParseAddr("192.168.50.23"), ObservedAt: h.now(), FreshUntil: h.now().Add(30 * time.Second)}
			edit(&e)
			return e, nil
		})
		plan, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
		if err != nil || plan.Route.State != "unavailable" || plan.Route.SourceAddress != "" || plan.Route.ObservedAt != nil {
			t.Fatalf("invalid evidence projected: %+v %v", plan, err)
		}
	}
}

func TestGatewayPreviewDoesNotInspectRouteBeforeBindingOrAfterCancel(t *testing.T) {
	h, _, osInspector := qualityFixture(t)
	calls := 0
	h.gatewayRouteInspector = routeInspectorFunc(func(context.Context, networkquality.GatewayPlanBinding, netip.Addr) (gatewayroute.Evidence, error) {
		calls++
		return gatewayroute.Evidence{}, nil
	})
	for _, target := range []string{"gateway.local", "8.8.8.8", "10.0.0.1"} {
		_, _ = h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: target})
	}
	osInspector.state.Flags = 0
	_, _ = h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
	if calls != 0 {
		t.Fatal("invalid target/binding reached route inspector")
	}
	h, _, _ = qualityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.gatewayRouteInspector = routeInspectorFunc(func(context.Context, networkquality.GatewayPlanBinding, netip.Addr) (gatewayroute.Evidence, error) {
		cancel()
		return gatewayroute.Evidence{}, nil
	})
	if plan, err := h.PreviewGatewayCheck(ctx, api.GatewayPlanParams{Target: "192.168.50.1"}); !errors.Is(err, context.Canceled) || plan.Mode != "" {
		t.Fatalf("canceled preview returned: %+v %v", plan, err)
	}
}
