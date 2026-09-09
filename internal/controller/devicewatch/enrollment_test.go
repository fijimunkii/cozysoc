package devicewatch

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type mapInspector map[string]InterfaceState

func (m mapInspector) Inspect(_ context.Context, name string) (InterfaceState, error) {
	state, ok := m[name]
	if !ok {
		return InterfaceState{}, errors.New("missing interface")
	}
	return state, nil
}

func TestScopeCandidatesExcludeUnsafeInterfacesAndSort(t *testing.T) {
	interfaces := []net.Interface{
		{Name: "utun3", Index: 8},
		{Name: "en1", Index: 5},
		{Name: "lo0", Index: 1},
		{Name: "en0", Index: 3},
	}
	inspector := mapInspector{
		"lo0":   {Name: "lo0", Index: 1, Flags: net.FlagUp | net.FlagLoopback, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		"en0":   {Name: "en0", Index: 3, Flags: net.FlagUp | net.FlagBroadcast, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.42/24")}},
		"en1":   {Name: "en1", Index: 5, Flags: net.FlagUp | net.FlagBroadcast, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.9/24")}},
		"utun3": {Name: "utun3", Index: 8, Flags: net.FlagUp | net.FlagPointToPoint, Prefixes: []netip.Prefix{netip.MustParsePrefix("100.64.0.2/24")}},
	}

	candidates, truncated, err := scopeCandidatesFromInterfaces(context.Background(), inspector, interfaces)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("small candidate set was marked truncated")
	}
	if len(candidates) != 2 || candidates[0].InterfaceName != "en0" || candidates[1].InterfaceName != "en1" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
	if candidates[0].Prefixes[0] != "192.168.1.0/24" || candidates[1].Prefixes[0] != "10.0.0.0/24" {
		t.Fatalf("candidate prefixes were not canonicalized: %+v", candidates)
	}
}

func TestScopeCandidatesHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := scopeCandidatesFromInterfaces(ctx, mapInspector{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled candidate listing error = %v", err)
	}
}
