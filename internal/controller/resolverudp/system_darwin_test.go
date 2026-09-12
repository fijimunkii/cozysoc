package resolverudp

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"testing"
)

func TestDarwinUDPABIAndCanceledOpen(t *testing.T) {
	if unix.SizeofCmsghdr != 12 || unix.SizeofInet6Pktinfo != 20 || unix.IPPROTO_IP != 0 || unix.IPPROTO_IPV6 != 41 || unix.IP_RECVDSTADDR != 7 || unix.IP_RECVIF != 20 || unix.AF_LINK != 18 || unix.IPV6_PKTINFO != 46 {
		t.Fatal("unsupported ancillary ABI")
	}
	_, _, r, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := openSystem(ctx, r, 50000); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	for _, e := range []error{unix.EAGAIN, unix.EINTR, unix.EADDRINUSE} {
		if socketError(e) != ErrUnavailable {
			t.Fatal("send failure changed retry policy")
		}
	}
}
