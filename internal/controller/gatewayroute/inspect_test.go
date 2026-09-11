package gatewayroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// All routing/interface evidence is synthetic. No test opens a route or IP socket.
func fixture() (networkquality.GatewayPlanBinding, localInterface, route, time.Time) {
	binding := networkquality.GatewayPlanBinding{ScopeID: "scope.home", InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24", "fe80::/64"}}
	local := localInterface{name: "en0", index: 7, flags: net.FlagUp, addresses: []netip.Prefix{netip.MustParsePrefix("192.168.50.23/24"), netip.MustParsePrefix("fe80::1234/64")}}
	r := route{destination: netip.MustParsePrefix("192.168.50.0/24"), interfaceName: "en0", interfaceIndex: 7, linkIndex: 7, source: netip.MustParseAddr("192.168.50.23"), flags: flagUp | flagDone | flagCloning}
	return binding, local, r, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
}

func TestInspectBracketsRouteWithExactAddressAndBindingChecks(t *testing.T) {
	binding, local, r, now := fixture()
	events := []string{}
	i := inspector{now: func() time.Time { return now }, readInterface: func(ctx context.Context, name string) (localInterface, error) {
		if name != "en0" {
			t.Fatal("caller changed enrolled interface")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no deadline")
		}
		events = append(events, "interface")
		return local, nil
	}, lookup: func(_ context.Context, target netip.Addr) (route, error) {
		if target.String() != "192.168.50.1" {
			t.Fatal("lookup target changed")
		}
		events = append(events, "route")
		return r, nil
	}}
	result, err := i.Inspect(context.Background(), binding, netip.MustParseAddr("192.168.50.1"))
	if err != nil || result.SourceAddress != r.source || result.InterfaceIndex != 7 || !result.ObservedAt.Equal(now) || !result.FreshUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if !reflect.DeepEqual(events, []string{"interface", "route", "interface"}) {
		t.Fatal(events)
	}
}

func TestRouteRefusesUnsafeOrMismatchedEvidence(t *testing.T) {
	cases := map[string]func(*localInterface, *route){
		"interface changed":      func(l *localInterface, _ *route) { l.name = "en1" },
		"index recycled":         func(l *localInterface, _ *route) { l.index++ },
		"down":                   func(l *localInterface, _ *route) { l.flags = net.FlagRunning },
		"vpn":                    func(l *localInterface, _ *route) { l.flags |= net.FlagPointToPoint },
		"loopback":               func(l *localInterface, _ *route) { l.flags |= net.FlagLoopback },
		"common link local only": func(l *localInterface, _ *route) { l.addresses = l.addresses[1:] },
		"new prefix": func(l *localInterface, _ *route) {
			l.addresses = append(l.addresses, netip.MustParsePrefix("10.1.2.3/24"))
		},
		"source removed in same prefix": func(l *localInterface, _ *route) { l.addresses[0] = netip.MustParsePrefix("192.168.50.24/24") },
		"source not in local subnet":    func(_ *localInterface, r *route) { r.source = netip.MustParseAddr("10.1.2.3") },
		"masked source is not assigned": func(_ *localInterface, r *route) { r.source = netip.MustParseAddr("192.168.50.0") },
		"target is this host": func(l *localInterface, _ *route) {
			l.addresses = append(l.addresses, netip.MustParsePrefix("192.168.50.1/24"))
		},
		"source is target":      func(_ *localInterface, r *route) { r.source = netip.MustParseAddr("192.168.50.1") },
		"off interface route":   func(_ *localInterface, r *route) { r.interfaceName = "utun0" },
		"wrong route index":     func(_ *localInterface, r *route) { r.interfaceIndex++ },
		"wrong link index":      func(_ *localInterface, r *route) { r.linkIndex++ },
		"gateway hop":           func(_ *localInterface, r *route) { r.flags |= 0x2; r.linkIndex = 0 },
		"wrong destination":     func(_ *localInterface, r *route) { r.destination = netip.MustParsePrefix("10.0.0.0/8") },
		"v6 destination":        func(_ *localInterface, r *route) { r.destination = netip.MustParsePrefix("fd00::/64") },
		"v6 source":             func(_ *localInterface, r *route) { r.source = netip.MustParseAddr("fd00::1") },
		"invalid address":       func(l *localInterface, _ *route) { l.addresses = []netip.Prefix{{}} },
		"oversized address set": func(l *localInterface, _ *route) { l.addresses = make([]netip.Prefix, 129) },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			binding, local, r, now := fixture()
			edit(&local, &r)
			i := inspector{now: func() time.Time { return now }, lookup: func(context.Context, netip.Addr) (route, error) { return r, nil }, readInterface: func(context.Context, string) (localInterface, error) { return local, nil }}
			got, err := i.Inspect(context.Background(), binding, netip.MustParseAddr("192.168.50.1"))
			if !errors.Is(err, ErrMismatch) || got != (Evidence{}) {
				t.Fatalf("unsafe evidence survived: %+v, %v", got, err)
			}
		})
	}
	for _, flag := range []uint32{0x8, 0x10, 0x20, 0x200, 0x1000, 0x200000, 0x400000, 0x800000, 0x2000000, 0x8000000, 0x80000000} {
		binding, local, r, now := fixture()
		r.flags |= flag
		if matchesRoute(binding, netip.MustParseAddr("192.168.50.1"), r, local, local, now) {
			t.Fatalf("unsafe flag %x accepted", flag)
		}
	}
}

func TestInspectRejectsChangeDuringLookupAndDoesNotRestampRoute(t *testing.T) {
	binding, local, r, now := fixture()
	calls := 0
	i := inspector{now: func() time.Time { return now }, lookup: func(context.Context, netip.Addr) (route, error) { return r, nil }, readInterface: func(context.Context, string) (localInterface, error) {
		calls++
		if calls == 2 {
			local.addresses[0] = netip.MustParsePrefix("192.168.50.24/24")
		}
		return local, nil
	}}
	if _, err := i.Inspect(context.Background(), binding, netip.MustParseAddr("192.168.50.1")); !errors.Is(err, ErrMismatch) {
		t.Fatal(err)
	}
	// Return independent snapshots so mutations cannot manufacture earlier evidence.
	_, local, _, now = fixture()
	start := now
	i.readInterface = func(context.Context, string) (localInterface, error) {
		now = now.Add(100 * time.Millisecond)
		return local, nil
	}
	got, err := i.Inspect(context.Background(), binding, netip.MustParseAddr("192.168.50.1"))
	if err != nil || !got.ObservedAt.Equal(start.Add(100*time.Millisecond)) || !got.FreshUntil.Equal(got.ObservedAt.Add(30*time.Second)) {
		t.Fatalf("restamped route: %+v %v", got, err)
	}
}

func TestInspectCancellationBoundsAndDependencyErrors(t *testing.T) {
	for _, mode := range []string{"pre-canceled", "after-read", "after-lookup", "after-recheck", "lookup-error", "read-error", "bad-clock", "slow", "bad-binding", "bad-target", "nil-dependency"} {
		t.Run(mode, func(t *testing.T) {
			binding, local, r, now := fixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, lookups := 0, 0
			target := netip.MustParseAddr("192.168.50.1")
			i := inspector{now: func() time.Time { return now }, lookup: func(context.Context, netip.Addr) (route, error) {
				lookups++
				if mode == "after-lookup" {
					cancel()
				}
				if mode == "lookup-error" {
					return route{}, ErrNoRoute
				}
				return r, nil
			}, readInterface: func(context.Context, string) (localInterface, error) {
				reads++
				if mode == "read-error" {
					return localInterface{}, ErrPermission
				}
				if (mode == "after-read" && reads == 1) || (mode == "after-recheck" && reads == 2) {
					cancel()
				}
				if reads == 2 {
					if mode == "bad-clock" {
						now = now.Add(-time.Second)
					}
					if mode == "slow" {
						now = now.Add(3 * time.Second)
					}
				}
				return local, nil
			}}
			if mode == "pre-canceled" {
				cancel()
			}
			if mode == "bad-binding" {
				binding.ScopeID = "<script>"
			}
			if mode == "bad-target" {
				target = netip.MustParseAddr("8.8.8.8")
			}
			if mode == "nil-dependency" {
				i.lookup = nil
			}
			got, err := i.Inspect(ctx, binding, target)
			if err == nil || got != (Evidence{}) {
				t.Fatalf("failure became evidence: %+v %v", got, err)
			}
			if mode == "pre-canceled" || mode == "bad-binding" || mode == "bad-target" || mode == "nil-dependency" {
				if reads != 0 || lookups != 0 {
					t.Fatal("invalid input reached OS")
				}
			}
		})
	}
}
