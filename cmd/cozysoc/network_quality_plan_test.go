package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func TestGatewayPlanControllerReviewIsNotConsent(t *testing.T) {
	h, store, inspector := qualityFixture(t)
	result, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 1 || result.Mode != "preview-only" || result.ExecutionAvailable || result.ConsentGranted ||
		result.Target.RoleVerified || result.Target.Role != "user-selected-gateway" || result.Target.Address != "192.168.50.1" || result.Target.Family != "ipv4" ||
		result.Binding.ScopeID != "scope.home" || result.Binding.InterfaceName != "en0" || result.Binding.InterfaceIndex != 7 || result.Method != "icmp-echo" {
		t.Fatalf("incorrect preview or invented authority: %+v", result)
	}
	if !result.CreatedAt.Equal(h.now()) || !result.ReviewExpiresAt.Equal(result.CreatedAt.Add(30*time.Second)) ||
		result.ProposedBudget.MaxAttempts != 3 || result.ProposedBudget.MaxICMPRequestBytes != 120 || result.ProposedBudget.MinRunIntervalMS != 60000 ||
		len(result.Limitations) != 7 || inspector.calls != 1 || !reflect.DeepEqual(inspector.names, []string{"en0"}) {
		t.Fatalf("missing context or fixed bounds: %+v", result)
	}
	if store.enrollMetadata != nil || store.setDevice != "" {
		t.Fatal("preview mutated storage")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"session_secret", "evidence_id", "latency_ms", "loss_percent", "execution_token"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unexpected field %s", forbidden)
		}
	}
	// Repeated reviews are pure reads, not implicit approval or accumulated grants.
	second, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
	if err != nil || !reflect.DeepEqual(result, second) {
		t.Fatalf("review changed state: %v", err)
	}
}

func TestGatewayPlanRejectsInvalidSelectionBeforeOSInspection(t *testing.T) {
	for _, target := range []string{"gateway.local", "8.8.8.8", "10.0.0.1", "192.168.50.0", "192.168.50.255", "::ffff:192.168.50.1"} {
		h, _, inspector := qualityFixture(t)
		result, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: target})
		if !errors.Is(err, localapi.ErrInvalidRead) || inspector.calls != 0 || !reflect.DeepEqual(result, api.GatewayCheckPlan{}) {
			t.Fatalf("invalid selection reached OS: %v", err)
		}
	}
}

func TestGatewayPlanRequiresFullCurrentUpBinding(t *testing.T) {
	for name, edit := range map[string]func(*qualityInspector){
		"down":           func(i *qualityInspector) { i.state.Flags = 0 },
		"new interface":  func(i *qualityInspector) { i.state.Name = "en1" },
		"new index":      func(i *qualityInspector) { i.state.Index++ },
		"moved network":  func(i *qualityInspector) { i.state.Prefixes[1] = netip.MustParsePrefix("192.168.60.0/24") },
		"removed prefix": func(i *qualityInspector) { i.state.Prefixes = i.state.Prefixes[:1] },
		"added prefix": func(i *qualityInspector) {
			i.state.Prefixes = append(i.state.Prefixes, netip.MustParsePrefix("10.0.0.0/24"))
		},
		"vpn":        func(i *qualityInspector) { i.state.Flags |= net.FlagPointToPoint },
		"loopback":   func(i *qualityInspector) { i.state.Flags |= net.FlagLoopback },
		"permission": func(i *qualityInspector) { i.err = os.ErrPermission },
		"source":     func(i *qualityInspector) { i.err = errors.New("private-source-error") },
	} {
		t.Run(name, func(t *testing.T) {
			h, _, inspector := qualityFixture(t)
			edit(inspector)
			result, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
			if !errors.Is(err, localapi.ErrGatewayPlanPrecondition) || !reflect.DeepEqual(result, api.GatewayCheckPlan{}) {
				t.Fatalf("invalid binding returned plan: %v", err)
			}
			if strings.Contains(err.Error(), "private-source") {
				t.Fatal("source error leaked")
			}
		})
	}
}

func TestGatewayPlanEnrollmentAndClockFailures(t *testing.T) {
	for name, edit := range map[string]func(*controllerAPIHandler, *fakeDeviceStore){
		"no enrollment": func(_ *controllerAPIHandler, s *fakeDeviceStore) { s.activeScopes = nil },
		"multiple enrollments": func(_ *controllerAPIHandler, s *fakeDeviceStore) {
			s.activeScopes = append(s.activeScopes, s.activeScopes[0])
		},
		"storage failure": func(_ *controllerAPIHandler, s *fakeDeviceStore) { s.activeErr = errors.New("private-database") },
		"invalid metadata": func(_ *controllerAPIHandler, s *fakeDeviceStore) {
			s.activeScopes[0].Metadata = json.RawMessage(`{"unknown":"private-value"}`)
		},
		"invalid scope": func(_ *controllerAPIHandler, s *fakeDeviceStore) { s.activeScopes[0].ID = "<script>" },
		"no source":     func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.networkInspector = nil },
		"no store":      func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.store = nil },
		"no clock":      func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.now = nil },
		"zero clock":    func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.now = func() time.Time { return time.Time{} } },
	} {
		t.Run(name, func(t *testing.T) {
			h, store, inspector := qualityFixture(t)
			edit(h, store)
			result, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"})
			if err == nil || inspector.calls != 0 || !reflect.DeepEqual(result, api.GatewayCheckPlan{}) {
				t.Fatalf("invalid setup returned plan: %v", err)
			}
			if name == "no enrollment" && !errors.Is(err, localapi.ErrReadTargetNotFound) {
				t.Fatalf("missing enrollment error: %v", err)
			}
		})
	}
	for _, elapsed := range []time.Duration{-time.Second, 6 * time.Second} {
		h, _, inspector := qualityFixture(t)
		now := h.now()
		inspector.after = func() { h.now = func() time.Time { return now.Add(elapsed) } }
		if result, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"}); err == nil || result.Mode != "" {
			t.Fatalf("bad sample clock: %v", err)
		}
	}
}

func TestGatewayPlanHonorsCancellation(t *testing.T) {
	h, _, inspector := qualityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.PreviewGatewayCheck(ctx, api.GatewayPlanParams{Target: "192.168.50.1"}); !errors.Is(err, context.Canceled) || inspector.calls != 0 {
		t.Fatalf("cancellation lost: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	inspector.after = cancel
	if result, err := h.PreviewGatewayCheck(ctx, api.GatewayPlanParams{Target: "192.168.50.1"}); !errors.Is(err, context.Canceled) || result.Mode != "" {
		t.Fatalf("canceled plan survived: %v", err)
	}
	inspector.after = nil
	inspector.err = context.DeadlineExceeded
	if _, err := h.PreviewGatewayCheck(context.Background(), api.GatewayPlanParams{Target: "192.168.50.1"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("source cancellation lost: %v", err)
	}
}
