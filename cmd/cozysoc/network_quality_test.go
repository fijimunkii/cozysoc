package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type qualityInspector struct {
	state devicewatch.InterfaceState
	err   error
	calls int
	names []string
	after func()
}

func (f *qualityInspector) Inspect(_ context.Context, name string) (devicewatch.InterfaceState, error) {
	f.calls++
	f.names = append(f.names, name)
	if f.after != nil {
		f.after()
	}
	return f.state, f.err
}

func qualityFixture(t *testing.T) (*controllerAPIHandler, *fakeDeviceStore, *qualityInspector) {
	t.Helper()
	binding := devicewatch.ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24", "fe80::/64"}}
	metadata, err := devicewatch.EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeDeviceStore{activeScopes: []domain.NetworkScope{{ID: "scope.home", Metadata: metadata}}}
	inspector := &qualityInspector{state: devicewatch.InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("fe80::/64"), netip.MustParsePrefix("192.168.50.23/24")},
	}}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// Deliberately no Device Watch control: a metadata read must not depend on
	// enabling, starting, or even querying the monitoring lifecycle.
	return &controllerAPIHandler{store: store, networkInspector: inspector, now: func() time.Time { return now }}, store, inspector
}

func TestLocalNetworkQualityAdministrativeStateAndMinimization(t *testing.T) {
	for _, up := range []bool{true, false} {
		t.Run(fmt.Sprint(up), func(t *testing.T) {
			h, store, inspector := qualityFixture(t)
			if !up {
				inspector.state.Flags = net.FlagRunning
			}
			result, err := h.LocalNetworkQuality(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantState := "check-succeeded"
			if !up {
				wantState = "issue-observed"
			}
			if !result.Enrolled || result.Observer == nil || result.Check == nil || result.Observer.ScopeID != "scope.home" ||
				result.Observer.InterfaceName != "en0" || result.Observer.InterfaceIndex != 7 || result.Check.AdministrativeUp == nil ||
				*result.Check.AdministrativeUp != up || result.Check.State != wantState || result.Check.Confidence != "limited" ||
				result.Check.Gap != "" || !strings.Contains(result.Check.Summary, "administratively") {
				t.Fatalf("unexpected result: %+v", result)
			}
			if inspector.calls != 1 || !reflect.DeepEqual(inspector.names, []string{"en0"}) ||
				store.enrollMetadata != nil || store.setDevice != "" {
				t.Fatal("read expanded scope or mutated configuration")
			}
			if result.Check.Layer != "local-link" || result.Check.Method != "interface-state" ||
				result.Check.Source != "os-interface-metadata" || result.Check.EvidenceID == "" ||
				!result.Check.CompletedAt.Equal(result.AsOf) || !result.Check.FreshUntil.Equal(result.AsOf.Add(localInterfaceFreshness)) {
				t.Fatalf("missing source or evidence time: %+v", result.Check)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"prefixes", "192.168.50", "fe80::", "hardware_address", "mean_latency", "loss_percent", "raw_flags", "session_secret", "internet-down"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("unexpected projection field/value %q: %s", forbidden, encoded)
				}
			}
			if len(result.Limitations) != 4 {
				t.Fatal("source limitations missing")
			}
		})
	}
}

func TestLocalNetworkQualityUnavailableAndBindingMatrix(t *testing.T) {
	cases := []struct {
		name string
		edit func(*qualityInspector)
		gap  string
	}{
		{"name changed", func(i *qualityInspector) { i.state.Name = "en1" }, "network-changed"},
		{"index changed", func(i *qualityInspector) { i.state.Index++ }, "network-changed"},
		{"prefix changed with common link local", func(i *qualityInspector) { i.state.Prefixes[1] = netip.MustParsePrefix("192.168.60.0/24") }, "network-changed"},
		{"prefix added", func(i *qualityInspector) {
			i.state.Prefixes = append(i.state.Prefixes, netip.MustParsePrefix("10.0.0.0/24"))
		}, "network-changed"},
		{"prefix removed", func(i *qualityInspector) { i.state.Prefixes = i.state.Prefixes[:1] }, "network-changed"},
		{"down without binding", func(i *qualityInspector) { i.state.Flags = 0; i.state.Prefixes = nil }, "network-changed"},
		{"loopback", func(i *qualityInspector) { i.state.Flags |= net.FlagLoopback }, "unsupported"},
		{"point to point", func(i *qualityInspector) { i.state.Flags |= net.FlagPointToPoint }, "unsupported"},
		{"permission denied", func(i *qualityInspector) { i.err = fmt.Errorf("secret-path: %w", os.ErrPermission) }, "permission-required"},
		{"missing interface", func(i *qualityInspector) { i.err = errors.New("secret-path: not found") }, "source-unavailable"},
		{"oversized metadata", func(i *qualityInspector) { i.state.Prefixes = make([]netip.Prefix, maxLocalInterfacePrefixes+1) }, "source-unavailable"},
		{"invalid metadata", func(i *qualityInspector) { i.state.Prefixes = []netip.Prefix{{}} }, "source-unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, inspector := qualityFixture(t)
			tc.edit(inspector)
			result, err := h.LocalNetworkQuality(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Check == nil || result.Check.State != "not-measured" || result.Check.Gap != tc.gap ||
				result.Check.Confidence != "unknown" || result.Check.AdministrativeUp != nil || result.Check.NextStep == "" ||
				result.Observer.InterfaceName != "en0" || result.Observer.InterfaceIndex != 7 {
				t.Fatalf("unverified data became a measurement: %+v", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "secret-path") || strings.Contains(string(encoded), "administrative_up") {
				t.Fatalf("unmeasured result leaked metrics/diagnostics: %s", encoded)
			}
		})
	}
}

func TestLocalNetworkQualityCanonicalPrefixesAndLinkLocalLimits(t *testing.T) {
	h, store, inspector := qualityFixture(t)
	inspector.state.Prefixes = append(inspector.state.Prefixes, netip.MustParsePrefix("192.168.50.99/24"))
	result, err := h.LocalNetworkQuality(context.Background())
	if err != nil || result.Check == nil || result.Check.AdministrativeUp == nil {
		t.Fatalf("same-prefix extra address should not change binding: %v", err)
	}
	metadata, err := devicewatch.EncodeScopeMetadata(devicewatch.ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"fe80::/64"}})
	if err != nil {
		t.Fatal(err)
	}
	store.activeScopes[0].Metadata = metadata
	inspector.state.Prefixes = []netip.Prefix{netip.MustParsePrefix("fe80::/64")}
	result, err = h.LocalNetworkQuality(context.Background())
	if err != nil || result.Check.State != "not-measured" || result.Check.Gap != "source-unavailable" {
		t.Fatalf("link-local-only binding acquired a quality conclusion: %+v, %v", result, err)
	}
}

func TestLocalNetworkQualityNoEnrollmentDoesNotInspect(t *testing.T) {
	h, store, inspector := qualityFixture(t)
	store.activeScopes = nil
	result, err := h.LocalNetworkQuality(context.Background())
	if err != nil || result.Enrolled || result.Check != nil || result.Observer != nil || inspector.calls != 0 || result.AsOf.IsZero() {
		t.Fatalf("unconfigured read invented evidence or inspected interfaces: %+v, %v", result, err)
	}
}

func TestLocalNetworkQualityFailsBeforeInspection(t *testing.T) {
	for name, edit := range map[string]func(*controllerAPIHandler, *fakeDeviceStore){
		"multiple scopes": func(_ *controllerAPIHandler, s *fakeDeviceStore) {
			s.activeScopes = append(s.activeScopes, s.activeScopes[0])
		},
		"storage error": func(_ *controllerAPIHandler, s *fakeDeviceStore) { s.activeErr = errors.New("read failed") },
		"malformed enrollment": func(_ *controllerAPIHandler, s *fakeDeviceStore) {
			s.activeScopes[0].Metadata = json.RawMessage(`{"device_watch":{"interface_name":"secret-path"}}`)
		},
		"malformed context": func(_ *controllerAPIHandler, s *fakeDeviceStore) { s.activeScopes[0].ID = "<script>" },
		"missing source":    func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.networkInspector = nil },
		"missing clock":     func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.now = nil },
		"zero clock":        func(h *controllerAPIHandler, _ *fakeDeviceStore) { h.now = func() time.Time { return time.Time{} } },
	} {
		t.Run(name, func(t *testing.T) {
			h, store, inspector := qualityFixture(t)
			edit(h, store)
			result, err := h.LocalNetworkQuality(context.Background())
			if err == nil || inspector.calls != 0 || !reflect.DeepEqual(result, api.LocalNetworkQuality{}) {
				t.Fatalf("invalid setup produced evidence: %+v, %v", result, err)
			}
			if strings.Contains(err.Error(), "secret-path") {
				t.Fatal("enrollment content leaked")
			}
		})
	}
}

func TestLocalNetworkQualityCancellationAndClock(t *testing.T) {
	h, _, inspector := qualityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.LocalNetworkQuality(ctx); !errors.Is(err, context.Canceled) || inspector.calls != 0 {
		t.Fatalf("pre-canceled read inspected the OS: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	inspector.after = cancel
	if result, err := h.LocalNetworkQuality(ctx); !errors.Is(err, context.Canceled) || result.Check != nil {
		t.Fatalf("canceled sample survived: %+v, %v", result, err)
	}
	for _, elapsed := range []time.Duration{-time.Second, 31 * time.Second} {
		h, _, _ := qualityFixture(t)
		start := h.now()
		calls := 0
		h.now = func() time.Time {
			calls++
			if calls == 1 {
				return start
			}
			return start.Add(elapsed)
		}
		if result, err := h.LocalNetworkQuality(context.Background()); err == nil || result.Check != nil {
			t.Fatalf("invalid sample timing survived: %+v, %v", result, err)
		}
	}
}
