package gatewayicmp

import (
	"context"
	"errors"
	"net/netip"
	"syscall"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
)

func platform() (func(context.Context, Request) (routeEvidence, error), func(context.Context, Request) (socket, error)) {
	inspector := gatewayroute.NewInspector()
	return func(ctx context.Context, r Request) (routeEvidence, error) {
		e, err := inspector.Inspect(ctx, r.Plan.Binding, r.Plan.Target)
		if err != nil {
			switch {
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				return routeEvidence{}, err
			case errors.Is(err, gatewayroute.ErrUnsupported):
				return routeEvidence{}, ErrUnsupported
			case errors.Is(err, gatewayroute.ErrPermission):
				return routeEvidence{}, ErrPermission
			case errors.Is(err, gatewayroute.ErrMismatch):
				return routeEvidence{}, ErrBinding
			default:
				return routeEvidence{}, ErrUnavailable
			}
		}
		return routeEvidence{name: e.InterfaceName, index: e.InterfaceIndex, source: e.SourceAddress,
			observedAt: e.ObservedAt, freshUntil: e.FreshUntil}, nil
	}, openSystem
}

type darwinSocket struct {
	fd      int
	request Request
	data    [maxPacketBytes]byte
	control [maxControlBytes]byte
}

func openSystem(ctx context.Context, r Request) (socket, error) {
	if err := contextError(ctx, time.Now()); err != nil {
		return nil, err
	}
	if syscall.SizeofCmsghdr != 12 || syscall.IPPROTO_IP != 0 || syscall.IP_RECVDSTADDR != 7 ||
		syscall.IP_RECVIF != 20 || syscall.AF_LINK != 18 {
		return nil, ErrUnsupported
	}
	var err error
	if r, err = normalize(r, time.Now().UTC()); err != nil {
		return nil, err
	}
	// No RAW socket, shell, DNS resolution, or fallback privileges. ForkLock makes
	// close-on-exec setup atomic with respect to Go subprocess creation.
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, socketError(err)
	}
	s := &darwinSocket{fd: fd, request: copyRequest(r)}
	fail := func(err error) (socket, error) { _ = s.close(); return nil, socketError(err) }
	if err := syscall.SetNonblock(fd, true); err != nil {
		return fail(err)
	}
	for _, option := range []struct{ level, name, value int }{
		{syscall.SOL_SOCKET, syscall.SO_RCVBUF, 8192},
		{syscall.SOL_SOCKET, syscall.SO_SNDBUF, 512},
		{syscall.SOL_SOCKET, syscall.SO_BROADCAST, 0},
		{syscall.SOL_SOCKET, syscall.SO_DONTROUTE, 0},
		{syscall.IPPROTO_IP, syscall.IP_HDRINCL, 0},
		{syscall.IPPROTO_IP, syscall.IP_STRIPHDR, 1},
		{syscall.IPPROTO_IP, syscall.IP_RECVDSTADDR, 1},
		{syscall.IPPROTO_IP, syscall.IP_RECVIF, 1},
		{syscall.IPPROTO_IP, syscall.IP_TTL, 1},
		{syscall.IPPROTO_IP, syscall.IP_BOUND_IF, r.Plan.Binding.InterfaceIndex},
	} {
		if err := syscall.SetsockoptInt(fd, option.level, option.name, option.value); err != nil {
			return fail(err)
		}
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: r.Source.As4()}); err != nil {
		return fail(err)
	}
	// Leave the socket unconnected: every send supplies the single stored numeric
	// target. There is no UDP-connect route trick and no destination-changing API.
	if err := s.verify(ctx); err != nil {
		_ = s.close()
		return nil, err
	}
	return s, nil
}

func (s *darwinSocket) verify(ctx context.Context) error {
	if err := contextError(ctx, time.Now()); err != nil {
		return err
	}
	if s.fd < 0 {
		return ErrUnavailable
	}
	local, err := syscall.Getsockname(s.fd)
	if err != nil {
		return socketError(err)
	}
	v4, ok := local.(*syscall.SockaddrInet4)
	if !ok || netip.AddrFrom4(v4.Addr) != s.request.Source || v4.Port != 0 {
		return ErrBinding
	}
	for _, option := range []struct {
		level, name, value int
		boolean            bool
	}{
		{syscall.SOL_SOCKET, syscall.SO_TYPE, syscall.SOCK_DGRAM, false},
		{syscall.SOL_SOCKET, syscall.SO_BROADCAST, 0, false},
		{syscall.SOL_SOCKET, syscall.SO_DONTROUTE, 0, false},
		{syscall.IPPROTO_IP, syscall.IP_BOUND_IF, s.request.Plan.Binding.InterfaceIndex, false},
		{syscall.IPPROTO_IP, syscall.IP_HDRINCL, 0, false},
		{syscall.IPPROTO_IP, syscall.IP_STRIPHDR, 1, true},
		{syscall.IPPROTO_IP, syscall.IP_RECVDSTADDR, 1, true},
		{syscall.IPPROTO_IP, syscall.IP_RECVIF, 1, true},
		{syscall.IPPROTO_IP, syscall.IP_TTL, 1, false},
	} {
		value, err := syscall.GetsockoptInt(s.fd, option.level, option.name)
		if err != nil {
			return socketError(err)
		}
		if (option.boolean && value == 0) || (!option.boolean && value != option.value) {
			return ErrBinding
		}
	}
	// Verify bounded effective buffers rather than assuming setsockopt's requested
	// bytes equal kernel accounting. The kernel may round up small buffer values.
	for _, option := range []int{syscall.SO_RCVBUF, syscall.SO_SNDBUF} {
		value, err := syscall.GetsockoptInt(s.fd, syscall.SOL_SOCKET, option)
		if err != nil {
			return socketError(err)
		}
		if value < packetBytes || value > 65536 {
			return ErrUnavailable
		}
	}
	return contextError(ctx, time.Now())
}

func (s *darwinSocket) send(ctx context.Context, packet []byte) error {
	if len(packet) != packetBytes || packet[0] != 8 || packet[1] != 0 || checksum(packet) != 0 {
		return ErrBinding
	}
	// Read back THIS socket's source/interface immediately before every send.
	if err := s.verify(ctx); err != nil {
		return err
	}
	if !time.Now().Before(s.request.Plan.ReviewExpiresAt) {
		return context.DeadlineExceeded
	}
	if err := contextError(ctx, time.Now()); err != nil {
		return err
	}
	err := syscall.Sendto(s.fd, packet, syscall.MSG_DONTWAIT, &syscall.SockaddrInet4{Addr: s.request.Plan.Target.As4()})
	if err != nil {
		return socketError(err)
	} // Even EINTR/EAGAIN is NOT retried.
	return nil
}

func (s *darwinSocket) receive(ctx context.Context) (datagram, error) {
	if err := contextError(ctx, time.Now()); err != nil {
		return datagram{}, err
	}
	n, controlN, flags, peer, err := syscall.Recvmsg(s.fd, s.data[:], s.control[:], syscall.MSG_DONTWAIT)
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
		return datagram{}, errWouldBlock
	}
	if err != nil {
		return datagram{}, socketError(err)
	}
	if n < 0 || n > len(s.data) || controlN < 0 || controlN > len(s.control) {
		return datagram{}, ErrUnavailable
	}
	d := datagram{data: s.data[:n], truncated: flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0}
	if p, ok := peer.(*syscall.SockaddrInet4); ok && p.Port == 0 {
		d.peer = netip.AddrFrom4(p.Addr)
	}
	local, index, ok := parseControl(s.control[:controlN])
	if ok {
		d.local, d.index = local, index
	}
	// Missing/invalid controls count against the receive budget, never as replies.
	return d, nil
}

func (s *darwinSocket) close() error {
	if s.fd < 0 {
		return nil
	}
	fd := s.fd
	s.fd = -1
	return syscall.Close(fd) // Never retry a possibly closed/recycled descriptor.
}

func socketError(err error) error {
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return ErrPermission
	}
	if errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.ENXIO) {
		return ErrBinding
	}
	return ErrUnavailable
}
