package checkroute

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"

	"golang.org/x/net/route"
)

const routeHeaderBytes = 92
const maxRouteMessageBytes = 4096
const routeVersion = 5
const routeGet = 4

func getRequest(target netip.Addr, pid, seq uint32) ([]byte, error) {
	if !target.IsValid() || target.Is4In6() || target.Zone() != "" || pid == 0 || seq == 0 {
		return nil, ErrUnavailable
	}
	var address route.Addr
	if target.Is4() {
		address = &route.Inet4Addr{IP: target.As4()}
	} else {
		address = &route.Inet6Addr{IP: target.As16()}
	}
	// Unscoped RTM_GET only. Do not force an interface to override the OS route.
	m := route.RouteMessage{Version: syscall.RTM_VERSION, Type: syscall.RTM_GET, ID: uintptr(pid), Seq: int(seq), Addrs: []route.Addr{syscall.RTAX_DST: address, syscall.RTAX_IFP: &route.LinkAddr{}}}
	b, err := m.Marshal()
	if err != nil || len(b) > maxRouteMessageBytes {
		return nil, ErrUnavailable
	}
	return b, nil
}

// The specialist parser handles typed addresses. This envelope validates complete
// Darwin datagrams, authoritative errno (offset 24, not rt_use at 28), and radix
// netmask family independently; those fields need stricter handling than ParseRIB.
func parseReply(b []byte, pid, seq uint32) (result observedRoute, matched bool, err error) {
	defer func() {
		if recover() != nil {
			result = observedRoute{}
			matched = false
			err = ErrUnavailable
		}
	}()
	invalid := func() (observedRoute, bool, error) { return observedRoute{}, false, ErrUnavailable }
	if len(b) < 4 || len(b) > maxRouteMessageBytes || int(binary.LittleEndian.Uint16(b)) != len(b) || b[2] != routeVersion {
		return invalid()
	}
	if b[3] != routeGet {
		return observedRoute{}, false, nil
	}
	if len(b) < routeHeaderBytes {
		return invalid()
	}
	if binary.LittleEndian.Uint32(b[16:]) != pid || binary.LittleEndian.Uint32(b[20:]) != seq {
		return observedRoute{}, false, nil
	}
	if errno := syscall.Errno(binary.LittleEndian.Uint32(b[24:])); errno != 0 {
		return observedRoute{}, true, safeSocketError(errno)
	}
	flags := int(binary.LittleEndian.Uint32(b[8:]))
	const allowed = syscall.RTF_UP | syscall.RTF_GATEWAY | syscall.RTF_HOST | syscall.RTF_DONE | syscall.RTF_CLONING | syscall.RTF_LLINFO | syscall.RTF_STATIC | syscall.RTF_PRCLONING | syscall.RTF_WASCLONED | syscall.RTF_PINNED | syscall.RTF_IFSCOPE | syscall.RTF_IFREF | syscall.RTF_ROUTER
	if flags&(syscall.RTF_UP|syscall.RTF_DONE) != syscall.RTF_UP|syscall.RTF_DONE || flags&^allowed != 0 {
		return observedRoute{}, true, ErrMismatch
	}
	mask := binary.LittleEndian.Uint32(b[12:])
	const required = 1<<syscall.RTAX_DST | 1<<syscall.RTAX_GATEWAY | 1<<syscall.RTAX_IFP | 1<<syscall.RTAX_IFA
	if mask&required != required || mask&^uint32(0xff) != 0 {
		return invalid()
	}
	normalized := append([]byte(nil), b...)
	offset := routeHeaderBytes
	family := 0
	for slot := 0; slot < 8; slot++ {
		if mask&(1<<slot) == 0 {
			continue
		}
		if offset+2 > len(b) {
			return invalid()
		}
		length := int(b[offset])
		step := (length + 3) &^ 3
		if length == 0 {
			step = 4
		}
		if offset+step > len(b) {
			return invalid()
		}
		if slot == syscall.RTAX_NETMASK || slot == syscall.RTAX_GENMASK {
			minimum := 4
			if family == syscall.AF_INET6 {
				minimum = 8
			}
			if family == 0 || (length != 0 && length < minimum) {
				return invalid()
			}
			// Radix-mask family/port/flow positions are mask bits, not sockaddr metadata.
			normalized[offset+1] = byte(family)
		} else {
			switch int(b[offset+1]) {
			case syscall.AF_INET:
				if length != 16 {
					return invalid()
				}
			case syscall.AF_INET6:
				if length != 28 {
					return invalid()
				}
				ip := netip.AddrFrom16([16]byte(b[offset+8 : offset+24]))
				if ip.IsLinkLocalUnicast() {
					embedded := binary.BigEndian.Uint16(b[offset+10 : offset+12])
					explicit := binary.LittleEndian.Uint32(b[offset+24 : offset+28])
					if embedded != 0 && explicit != 0 && uint32(embedded) != explicit {
						return invalid()
					}
				}
			case syscall.AF_LINK:
				if length < 8 || 8+int(b[offset+5])+int(b[offset+6])+int(b[offset+7]) > length {
					return invalid()
				}
			default:
				return invalid()
			}
		}
		if slot == syscall.RTAX_DST {
			family = int(b[offset+1])
			if family != syscall.AF_INET && family != syscall.AF_INET6 {
				return invalid()
			}
		}
		offset += step
	}
	if offset != len(b) {
		return invalid()
	}
	messages, e := route.ParseRIB(route.RIBTypeRoute, normalized)
	if e != nil || len(messages) != 1 {
		return invalid()
	}
	m, ok := messages[0].(*route.RouteMessage)
	if !ok || len(m.Addrs) < syscall.RTAX_IFA+1 {
		return invalid()
	}
	// m.Err reads a different ABI offset in this dependency; raw errno above is authoritative.
	dst, dz := ipAddress(m.Addrs[syscall.RTAX_DST])
	source, sz := ipAddress(m.Addrs[syscall.RTAX_IFA])
	iface, ok := m.Addrs[syscall.RTAX_IFP].(*route.LinkAddr)
	if !ok || iface.Name == "" || iface.Index < 1 || iface.Index != m.Index || !dst.IsValid() || !source.IsValid() || dst.Is4() != source.Is4() || dz != 0 || sz != 0 {
		return invalid()
	}
	bits := dst.BitLen()
	if a := m.Addrs[syscall.RTAX_NETMASK]; a != nil {
		address, _ := ipAddress(a)
		if !address.IsValid() || address.Is4() != dst.Is4() {
			return invalid()
		}
		raw := address.AsSlice()
		ones, width := net.IPMask(raw).Size()
		if width != dst.BitLen() {
			return invalid()
		}
		bits = ones
	} else if flags&syscall.RTF_HOST == 0 {
		return invalid()
	}
	if flags&syscall.RTF_HOST != 0 && bits != dst.BitLen() {
		return invalid()
	}
	prefix := netip.PrefixFrom(dst, bits)
	if prefix != prefix.Masked() {
		return invalid()
	}
	r := observedRoute{destination: prefix, source: source, name: iface.Name, index: iface.Index}
	if flags&syscall.RTF_GATEWAY != 0 {
		r.gateway, r.gatewayZone = ipAddress(m.Addrs[syscall.RTAX_GATEWAY])
		if !r.gateway.IsValid() {
			return invalid()
		}
	} else {
		link, ok := m.Addrs[syscall.RTAX_GATEWAY].(*route.LinkAddr)
		if !ok || link.Index != iface.Index {
			return invalid()
		}
		r.linkIndex = link.Index
	}
	return r, true, nil
}
func ipAddress(a route.Addr) (netip.Addr, int) {
	switch a := a.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP), 0
	case *route.Inet6Addr:
		return netip.AddrFrom16(a.IP), a.ZoneID
	}
	return netip.Addr{}, 0
}
