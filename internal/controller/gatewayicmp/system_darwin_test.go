package gatewayicmp

import (
	"context"
	"errors"
	"syscall"
	"testing"
)

// No test opens a real ICMP socket, including tests run on a Mac.
func TestDarwinABIAndCanceledSocketConstruction(t *testing.T) {
	if syscall.SizeofCmsghdr != 12 || syscall.IP_RECVDSTADDR != 7 || syscall.IP_RECVIF != 20 || syscall.AF_LINK != 18 {
		t.Fatal("unsupported ancillary ABI")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openSystem(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := openSystem(context.Background(), Request{}); !errors.Is(err, ErrBinding) {
		t.Fatal(err)
	}
	for in, want := range map[error]error{syscall.EPERM: ErrPermission, syscall.EACCES: ErrPermission, syscall.EADDRNOTAVAIL: ErrBinding, syscall.ENXIO: ErrBinding, syscall.EINTR: ErrUnavailable, syscall.EAGAIN: ErrUnavailable} {
		if socketError(in) != want {
			t.Fatal("unsafe error mapping")
		}
	}
	s := &darwinSocket{fd: -1}
	if err := s.verify(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
}
