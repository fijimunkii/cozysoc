package httpstcp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The private primitive uses loopback only in this OS socket fixture. Production
// entry points require the HTTPS plan and unscoped route inspector, which reject
// loopback targets/interfaces. This test makes no physical-interface claim.
func TestNativeTCPBindingReadbackAndMutation(t *testing.T) {
	iface, err := net.InterfaceByName("lo0")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ network, address string }{{"tcp4", "127.0.0.1:0"}, {"tcp6", "[::1]:0"}} {
		t.Run(tc.network, func(t *testing.T) {
			listener, err := net.Listen(tc.network, tc.address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					accepted <- conn
				}
				close(accepted)
			}()
			endpoint := listener.Addr().(*net.TCPAddr).AddrPort()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err := connectBound(ctx, endpoint.Addr(), endpoint, iface.Index, func() bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			server := <-accepted
			if server == nil {
				t.Fatal("accept failed")
			}
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(time.Second))
			if _, err := conn.Write([]byte("test")); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 4)
			if _, err := io.ReadFull(server, got); err != nil || string(got) != "test" {
				t.Fatal("native transfer failed", err)
			}
			bound := conn.(*boundConn)
			// Changing any expected binding or native option must prevent another write.
			originalPeer := bound.endpoint
			bound.endpoint = netip.AddrPortFrom(endpoint.Addr(), endpoint.Port()+1)
			if _, err := bound.Write([]byte("x")); !errors.Is(err, ErrBinding) {
				t.Fatal("peer mismatch ignored", err)
			}
			bound.endpoint = originalPeer
			originalSource := bound.source
			bound.source = netip.MustParseAddr("192.0.2.10")
			if _, err := bound.Write([]byte("x")); !errors.Is(err, ErrBinding) {
				t.Fatal("source mismatch ignored", err)
			}
			bound.source = originalSource
			raw, err := bound.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var changed error
			err = raw.Control(func(fd uintptr) { changed = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_KEEPALIVE, 1) })
			if err != nil || changed != nil {
				t.Fatal(err, changed)
			}
			if _, err := bound.Write([]byte("x")); !errors.Is(err, ErrBinding) {
				t.Fatal("changed socket option ignored", err)
			}
		})
	}
}
func TestSocketErrorsAndAddressValidation(t *testing.T) {
	for _, err := range []error{unix.EPERM, unix.EACCES} {
		if socketError(err) != ErrPermission {
			t.Fatal("lost permission failure")
		}
	}
	if socketError(unix.ECONNREFUSED) != ErrUnavailable {
		t.Fatal("unbounded socket error")
	}
	if socketAddress(&unix.SockaddrInet6{Port: 443, ZoneId: 7}).IsValid() || socketAddress(&unix.SockaddrInet4{Port: -1}).IsValid() {
		t.Fatal("invalid socket address")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openSystem(ctx, fixture(t)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
