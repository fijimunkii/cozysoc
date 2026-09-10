package gatewayroute

import (
	"context"
	"errors"
	"net/netip"
	"syscall"
	"testing"
)

func TestDarwinRouteABIAndSafeErrors(t *testing.T) {
	if syscall.SizeofRtMsghdr != routeHeaderBytes || syscall.RTM_VERSION != routeVersion || syscall.RTM_GET != routeGet || syscall.AF_LINK != afLink || syscall.AF_INET != afInet {
		t.Fatal("unsupported Darwin route ABI")
	}
	for err, want := range map[error]error{syscall.EPERM: ErrPermission, syscall.EACCES: ErrPermission, syscall.ESRCH: ErrNoRoute, syscall.ENETUNREACH: ErrNoRoute, syscall.EHOSTUNREACH: ErrNoRoute, syscall.EINVAL: ErrUnavailable, nil: ErrUnavailable} {
		if safeSocketError(err) != want {
			t.Fatal("unbounded error mapping")
		}
	}
	// A canceled lookup must not open even a routing socket.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lookupSystem(ctx, netip.MustParseAddr("192.168.50.1")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
