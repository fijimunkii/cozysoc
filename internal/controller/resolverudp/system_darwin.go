package resolverudp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"syscall"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/resolverroute"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverwire"
	"golang.org/x/sys/unix"
)

func platform() (func(context.Context, resolverrun.Request) (resolverrun.Selection, error), func(context.Context, resolverrun.Request, uint16) (socket, error)) {
	i := resolverroute.NewInspector()
	return func(ctx context.Context, r resolverrun.Request) (resolverrun.Selection, error) {
		d := r.Selection.Plan.Disclosure()
		selected, e := i.Inspect(ctx, resolverroute.Enrollment{Observer: d.Binding.Observer, Prefixes: d.Binding.Prefixes}, d.Configuration)
		if e != nil {
			switch {
			case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
				return selected, e
			case errors.Is(e, resolverroute.ErrUnsupported):
				return selected, ErrUnsupported
			case errors.Is(e, resolverroute.ErrPermission):
				return selected, ErrPermission
			case errors.Is(e, resolverroute.ErrMismatch):
				return selected, ErrBinding
			default:
				return selected, ErrUnavailable
			}
		}
		return selected, nil
	}, openSystem
}

type option struct {
	level, name, value int
	boolean            bool
}
type darwinSocket struct {
	fd      int
	r       resolverrun.Request
	port    uint16
	options []option
	data    [513]byte
	control [maxControlBytes]byte
}

func openSystem(ctx context.Context, r resolverrun.Request, port uint16) (socket, error) {
	if e := contextError(ctx); e != nil {
		return nil, e
	}
	if !r.Selection.Plan.Current(time.Now()) || port < 49152 {
		return nil, ErrBinding
	}
	if unix.SizeofCmsghdr != 12 || unix.SizeofInet6Pktinfo != 20 {
		return nil, ErrUnsupported
	}
	d := r.Selection.Plan.Disclosure()
	family := unix.AF_INET
	if d.Binding.Source.Is6() {
		family = unix.AF_INET6
	}
	syscall.ForkLock.RLock()
	fd, e := unix.Socket(family, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if e == nil {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if e != nil {
		return nil, socketError(e)
	}
	s := &darwinSocket{fd: fd, r: r, port: port}
	fail := func(e error) (socket, error) { _ = s.close(); return nil, socketError(e) }
	if e := unix.SetNonblock(fd, true); e != nil {
		return fail(e)
	}
	s.options = []option{{unix.SOL_SOCKET, unix.SO_REUSEADDR, 0, false}, {unix.SOL_SOCKET, unix.SO_REUSEPORT, 0, false}, {unix.SOL_SOCKET, unix.SO_BROADCAST, 0, false}, {unix.SOL_SOCKET, unix.SO_DONTROUTE, 0, false}}
	if family == unix.AF_INET {
		s.options = append(s.options, option{unix.IPPROTO_IP, unix.IP_BOUND_IF, d.Binding.Observer.InterfaceIndex, false}, option{unix.IPPROTO_IP, unix.IP_RECVDSTADDR, 1, true}, option{unix.IPPROTO_IP, unix.IP_RECVIF, 1, true})
	} else {
		s.options = append(s.options, option{unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, d.Binding.Observer.InterfaceIndex, false}, option{unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1, true}, option{unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, 1, true})
	}
	for _, o := range s.options {
		if e := unix.SetsockoptInt(fd, o.level, o.name, o.value); e != nil {
			return fail(e)
		}
	}
	for _, o := range []option{{unix.SOL_SOCKET, unix.SO_RCVBUF, 8192, false}, {unix.SOL_SOCKET, unix.SO_SNDBUF, 1024, false}} {
		if e := unix.SetsockoptInt(fd, o.level, o.name, o.value); e != nil {
			return fail(e)
		}
	}
	if e := unix.Bind(fd, sockaddr(netip.AddrPortFrom(d.Binding.Source, port))); e != nil {
		return fail(e)
	}
	if e := s.verify(ctx); e != nil {
		_ = s.close()
		return nil, e
	}
	return s, nil
}
func sockaddr(p netip.AddrPort) unix.Sockaddr {
	if p.Addr().Is4() {
		return &unix.SockaddrInet4{Addr: p.Addr().As4(), Port: int(p.Port())}
	}
	return &unix.SockaddrInet6{Addr: p.Addr().As16(), Port: int(p.Port())}
}
func address(sa unix.Sockaddr) netip.AddrPort {
	switch a := sa.(type) {
	case *unix.SockaddrInet4:
		return netip.AddrPortFrom(netip.AddrFrom4(a.Addr), uint16(a.Port))
	case *unix.SockaddrInet6:
		if a.ZoneId == 0 {
			return netip.AddrPortFrom(netip.AddrFrom16(a.Addr), uint16(a.Port))
		}
	}
	return netip.AddrPort{}
}
func (s *darwinSocket) verify(ctx context.Context) error {
	if e := contextError(ctx); e != nil {
		return e
	}
	if s.fd < 0 {
		return ErrUnavailable
	}
	d := s.r.Selection.Plan.Disclosure()
	local, e := unix.Getsockname(s.fd)
	if e != nil {
		return socketError(e)
	}
	if address(local) != netip.AddrPortFrom(d.Binding.Source, s.port) {
		return ErrBinding
	}
	if value, e := unix.GetsockoptInt(s.fd, unix.SOL_SOCKET, unix.SO_TYPE); e != nil || value != unix.SOCK_DGRAM {
		return ErrBinding
	}
	for _, o := range s.options {
		value, e := unix.GetsockoptInt(s.fd, o.level, o.name)
		if e != nil {
			return socketError(e)
		}
		if (o.boolean && value == 0) || (!o.boolean && value != o.value) {
			return ErrBinding
		}
	}
	for _, o := range []int{unix.SO_RCVBUF, unix.SO_SNDBUF} {
		value, e := unix.GetsockoptInt(s.fd, unix.SOL_SOCKET, o)
		if e != nil || value < 512 || value > 65536 {
			return ErrUnavailable
		}
	}
	return contextError(ctx)
}
func (s *darwinSocket) send(ctx context.Context, packet []byte) (bool, error) {
	if len(packet) < 12 {
		return false, ErrBinding
	}
	d := s.r.Selection.Plan.Disclosure()
	q, e := resolverwire.NewQuery(d.Configuration.Endpoint, d.Configuration.Name, d.Configuration.Selection.QueryType, binary.BigEndian.Uint16(packet))
	if e != nil || !bytes.Equal(q.Bytes(), packet) || len(packet) != d.Budget.MaxRequestBytes {
		return false, ErrBinding
	}
	if e := s.verify(ctx); e != nil {
		return false, e
	}
	if !s.r.Selection.Plan.Current(time.Now()) {
		return false, context.DeadlineExceeded
	}
	if e := contextError(ctx); e != nil {
		return false, e
	}
	// One syscall, numeric pinned endpoint; no connect, resolver call or retry.
	if e := unix.Sendto(s.fd, packet, unix.MSG_DONTWAIT, sockaddr(d.Configuration.Endpoint)); e != nil {
		return true, socketError(e)
	}
	return true, nil
}
func (s *darwinSocket) receive(ctx context.Context) (datagram, error) {
	if e := contextError(ctx); e != nil {
		return datagram{}, e
	}
	n, cn, flags, peer, e := unix.Recvmsg(s.fd, s.data[:], s.control[:], unix.MSG_DONTWAIT)
	if errors.Is(e, unix.EAGAIN) || errors.Is(e, unix.EINTR) {
		return datagram{}, errWouldBlock
	}
	if e != nil {
		return datagram{}, socketError(e)
	}
	if n < 0 || n > len(s.data) || cn < 0 || cn > len(s.control) {
		return datagram{}, ErrUnavailable
	}
	local, index, ok := parseControl(s.control[:cn], s.r.Selection.Plan.Disclosure().Binding.Source.Is6())
	d := datagram{data: s.data[:n], peer: address(peer), truncated: flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0}
	if ok {
		d.local = local
		d.index = index
	}
	return d, nil
}
func (s *darwinSocket) close() error {
	if s.fd < 0 {
		return nil
	}
	fd := s.fd
	s.fd = -1
	return unix.Close(fd)
}
func socketError(e error) error {
	if errors.Is(e, unix.EACCES) || errors.Is(e, unix.EPERM) {
		return ErrPermission
	}
	if errors.Is(e, unix.EADDRNOTAVAIL) || errors.Is(e, unix.ENXIO) {
		return ErrBinding
	}
	return ErrUnavailable
}
