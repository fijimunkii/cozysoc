package devicewatch

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type fakeInspector struct {
	state InterfaceState
	err   error
}

func (f fakeInspector) Inspect(context.Context, string) (InterfaceState, error) {
	return f.state, f.err
}

func TestCaptureAndValidateScopeBinding(t *testing.T) {
	inspector := fakeInspector{state: InterfaceState{
		Name:  "en0",
		Index: 7,
		Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
		Prefixes: []netip.Prefix{
			netip.MustParsePrefix("192.168.1.42/24"),
			netip.MustParsePrefix("fe80::1234/64"),
		},
	}}
	binding, err := CaptureScopeBinding(context.Background(), inspector, "en0")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseScopeBinding(domain.NetworkScope{ID: "scope.home", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.InterfaceName != binding.InterfaceName || parsed.InterfaceIndex != binding.InterfaceIndex || len(parsed.Prefixes) != len(binding.Prefixes) {
		t.Fatalf("scope binding round trip changed: %+v", parsed)
	}
	if _, err := ValidateCurrentScope(context.Background(), inspector, binding); err != nil {
		t.Fatal(err)
	}
	if !AddressInScope(binding, netip.MustParseAddr("192.168.1.9")) || !AddressInScope(binding, netip.MustParseAddr("fe80::2%en0")) {
		t.Fatal("address in enrolled prefix was rejected")
	}
	if AddressInScope(binding, netip.MustParseAddr("10.0.0.1")) {
		t.Fatal("out-of-scope address was accepted")
	}
}

func TestScopeChangeRequiresRevalidation(t *testing.T) {
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	changed := fakeInspector{state: InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/24")},
	}}
	_, err := ValidateCurrentScope(context.Background(), changed, binding)
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("scope change error = %v", err)
	}
}

func TestPointToPointInterfaceIsExcluded(t *testing.T) {
	inspector := fakeInspector{state: InterfaceState{
		Name: "utun3", Index: 12, Flags: net.FlagUp | net.FlagPointToPoint,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.10.0.2/24")},
	}}
	_, err := CaptureScopeBinding(context.Background(), inspector, "utun3")
	if !errors.Is(err, ErrUnsupportedInterface) {
		t.Fatalf("point-to-point capture error = %v", err)
	}
}
