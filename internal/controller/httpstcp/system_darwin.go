package httpstcp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	"golang.org/x/sys/unix"
)

type socketOption struct{ level, name, value int }

func bindingOptions(source netip.Addr, index int) []socketOption {
	options := []socketOption{{unix.SOL_SOCKET, unix.SO_KEEPALIVE, 0}, {unix.SOL_SOCKET, unix.SO_REUSEADDR, 0}, {unix.SOL_SOCKET, unix.SO_REUSEPORT, 0}, {unix.SOL_SOCKET, unix.SO_DONTROUTE, 0}}
	if source.Is4() {
		options = append(options, socketOption{unix.IPPROTO_IP, unix.IP_BOUND_IF, index})
	} else {
		options = append(options, socketOption{unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, index}, socketOption{unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1})
	}
	return options
}
func socketError(err error) error {
	if errors.Is(err, ErrBinding) {
		return ErrBinding
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return context.DeadlineExceeded
	}
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
		return ErrPermission
	}
	return ErrUnavailable
}
func openSystem(ctx context.Context, s httpsroute.Selection) (net.Conn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !s.Plan.Current(time.Now()) {
		return nil, ErrBinding
	}
	d := s.Plan.Disclosure()
	return connectBound(ctx, d.Binding.Source, d.Configuration.Endpoint, d.Binding.Observer.InterfaceIndex, func() bool { return s.Plan.Current(time.Now()) })
}

// Private transport primitive; openSystem supplies a validated immutable selection.
func connectBound(ctx context.Context, source netip.Addr, endpoint netip.AddrPort, index int, valid func() bool) (net.Conn, error) {
	if !source.IsValid() || !endpoint.IsValid() || source.Is4() != endpoint.Addr().Is4() || index <= 0 || valid == nil || !valid() {
		return nil, ErrBinding
	}
	network := "tcp4"
	if source.Is6() {
		network = "tcp6"
	}
	dialer := net.Dialer{Timeout: 2 * time.Second, KeepAlive: -1, LocalAddr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(source, 0))}
	dialer.ControlContext = func(ctx context.Context, gotNetwork, address string, raw syscall.RawConn) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !valid() || gotNetwork != network || address != endpoint.String() {
			return ErrBinding
		}
		var failure error
		err := raw.Control(func(fd uintptr) {
			for _, o := range bindingOptions(source, index) {
				if err := unix.SetsockoptInt(int(fd), o.level, o.name, o.value); err != nil {
					failure = socketError(err)
					return
				}
			}
			failure = verifyOptions(int(fd), source, index)
		})
		if err != nil {
			return socketError(err)
		}
		return failure
	}
	// Numeric address and an explicit family provide exactly one destination.
	conn, err := dialer.DialContext(ctx, network, endpoint.String())
	if err != nil {
		return nil, socketError(err)
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		return nil, ErrUnavailable
	}
	bound := &boundConn{TCPConn: tcp, source: source, endpoint: endpoint, index: index, valid: valid}
	if err := bound.verify(); err != nil {
		conn.Close()
		return nil, err
	}
	if ctx.Err() != nil {
		conn.Close()
		return nil, ctx.Err()
	}
	return bound, nil
}
func verifyOptions(fd int, source netip.Addr, index int) error {
	if value, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE); err != nil || value != unix.SOCK_STREAM {
		return ErrBinding
	}
	for _, o := range bindingOptions(source, index) {
		if value, err := unix.GetsockoptInt(fd, o.level, o.name); err != nil || value != o.value {
			return ErrBinding
		}
	}
	return nil
}

type boundConn struct {
	*net.TCPConn
	source   netip.Addr
	endpoint netip.AddrPort
	index    int
	valid    func() bool
}

func (c *boundConn) verify() error {
	if !c.valid() {
		return ErrBinding
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return socketError(err)
	}
	var failure error
	err = raw.Control(func(fd uintptr) {
		if failure = verifyOptions(int(fd), c.source, c.index); failure != nil {
			return
		}
		local, e := unix.Getsockname(int(fd))
		if e != nil {
			failure = socketError(e)
			return
		}
		peer, e := unix.Getpeername(int(fd))
		if e != nil {
			failure = socketError(e)
			return
		}
		l := socketAddress(local)
		if !l.IsValid() || l.Port() == 0 || l.Addr() != c.source || socketAddress(peer) != c.endpoint {
			failure = ErrBinding
		}
	})
	if err != nil {
		return socketError(err)
	}
	return failure
}
func (c *boundConn) Write(p []byte) (int, error) {
	if err := c.verify(); err != nil {
		return 0, err
	}
	return c.TCPConn.Write(p)
}
func socketAddress(sa unix.Sockaddr) netip.AddrPort {
	switch a := sa.(type) {
	case *unix.SockaddrInet4:
		if a.Port > 0 && a.Port <= 65535 {
			return netip.AddrPortFrom(netip.AddrFrom4(a.Addr), uint16(a.Port))
		}
	case *unix.SockaddrInet6:
		if a.ZoneId == 0 && a.Port > 0 && a.Port <= 65535 {
			return netip.AddrPortFrom(netip.AddrFrom16(a.Addr), uint16(a.Port))
		}
	}
	return netip.AddrPort{}
}
