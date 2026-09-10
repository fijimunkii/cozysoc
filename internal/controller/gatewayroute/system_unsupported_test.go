//go:build !darwin

package gatewayroute

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestUnsupportedDoesNotInspectAnything(t *testing.T) {
	b, _, _, _ := fixture()
	got, err := NewInspector().Inspect(context.Background(), b, netip.MustParseAddr("192.168.50.1"))
	if !errors.Is(err, ErrUnsupported) || got != (Evidence{}) {
		t.Fatalf("%+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewInspector().Inspect(ctx, b, netip.MustParseAddr("192.168.50.1")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
