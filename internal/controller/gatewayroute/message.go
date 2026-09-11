package gatewayroute

import (
	"encoding/binary"
	"net"
	"net/netip"
	"regexp"
)

// Darwin's public rt_msghdr / sockaddr ABI. Both supported Darwin Go targets are
// little endian. Kept platform-independent so malformed-message tests and fuzzing
// run on ordinary CI. The Darwin adapter checks the ABI constants before use.
const routeHeaderBytes = 92
const maxRouteMessageBytes = 4096
const routeVersion = 5
const routeGet = 4
const afInet = 2
const afLink = 18
const (
	flagUp        = 0x1
	flagHost      = 0x4
	flagDone      = 0x40
	flagCloning   = 0x100
	flagLLInfo    = 0x400
	flagStatic    = 0x800
	flagPrCloning = 0x10000
	flagWasCloned = 0x20000
	flagPinned    = 0x100000
	flagIfScope   = 0x1000000
	flagIfRef     = 0x4000000
	flagRouter    = 0x10000000
)

var routeInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

func getRequest(target netip.Addr, pid, seq uint32) ([]byte, error) {
	if !target.Is4() || !target.IsPrivate() || pid == 0 || seq == 0 {
		return nil, ErrUnavailable
	}
	// Only RTM_GET. Unscoped index/flags are zero. Request IFP so the kernel also
	// returns IFA; supply no caller-selected interface, source, gateway, or metrics.
	msg := make([]byte, routeHeaderBytes+16+20)
	binary.LittleEndian.PutUint16(msg, uint16(len(msg)))
	msg[2], msg[3] = routeVersion, routeGet
	binary.LittleEndian.PutUint32(msg[12:], 1<<0|1<<4)
	binary.LittleEndian.PutUint32(msg[16:], pid)
	binary.LittleEndian.PutUint32(msg[20:], seq)
	msg[routeHeaderBytes], msg[routeHeaderBytes+1] = 16, afInet
	address := target.As4()
	copy(msg[routeHeaderBytes+4:], address[:])
	msg[routeHeaderBytes+16], msg[routeHeaderBytes+17] = 20, afLink
	return msg, nil
}

// Matching replies are checked as complete bounded datagrams, including every
// sockaddr length/alignment. Foreign PID/sequence messages never become evidence.
func parseReply(msg []byte, pid, seq uint32) (route, bool, error) {
	invalid := func() (route, bool, error) { return route{}, false, ErrUnavailable }
	if len(msg) < 4 || len(msg) > maxRouteMessageBytes || int(binary.LittleEndian.Uint16(msg)) != len(msg) || msg[2] != routeVersion {
		return invalid()
	}
	if msg[3] != routeGet {
		return route{}, false, nil
	}
	if len(msg) < routeHeaderBytes {
		return invalid()
	}
	if binary.LittleEndian.Uint32(msg[16:]) != pid || binary.LittleEndian.Uint32(msg[20:]) != seq {
		return route{}, false, nil
	}
	switch binary.LittleEndian.Uint32(msg[24:]) {
	case 0:
	case 1, 13:
		return route{}, true, ErrPermission
	case 3, 51, 65:
		return route{}, true, ErrNoRoute
	default:
		return route{}, true, ErrUnavailable
	}
	flags := binary.LittleEndian.Uint32(msg[8:])
	if flags&flagDone == 0 {
		return invalid()
	}
	mask := binary.LittleEndian.Uint32(msg[12:])
	const required = 1<<0 | 1<<1 | 1<<4 | 1<<5
	if mask&required != required || mask&^uint32(0xff) != 0 {
		return invalid()
	}
	var addrs [8][]byte
	offset := routeHeaderBytes
	for bit := range addrs {
		if mask&(1<<bit) == 0 {
			continue
		}
		if offset >= len(msg) {
			return invalid()
		}
		length := int(msg[offset])
		step := (length + 3) &^ 3
		if length == 0 {
			step = 4
		}
		if offset+step > len(msg) || (length < 2 && bit != 2) {
			return invalid()
		}
		addrs[bit] = msg[offset : offset+length]
		offset += step
	}
	if offset != len(msg) {
		return invalid()
	}
	dst, ok := ipv4Sockaddr(addrs[0])
	if !ok {
		return invalid()
	}
	source, ok := ipv4Sockaddr(addrs[5])
	if !ok {
		return invalid()
	}
	iface := addrs[4]
	if len(iface) < 8 || iface[1] != afLink {
		return invalid()
	}
	nameLength, addressLength, selectorLength := int(iface[5]), int(iface[6]), int(iface[7])
	if 8+nameLength+addressLength+selectorLength > len(iface) || nameLength == 0 {
		return invalid()
	}
	name := string(iface[8 : 8+nameLength])
	index := int(binary.LittleEndian.Uint16(iface[2:]))
	if !routeInterfacePattern.MatchString(name) || index == 0 || index != int(binary.LittleEndian.Uint16(msg[4:])) {
		return invalid()
	}
	bits := 32
	if flags&flagHost == 0 {
		if mask&(1<<2) == 0 {
			return invalid()
		}
		var valid bool
		bits, valid = ipv4Mask(addrs[2])
		if !valid {
			return invalid()
		}
	} else if mask&(1<<2) != 0 {
		if value, valid := ipv4Mask(addrs[2]); !valid || value != 32 {
			return invalid()
		}
	}
	prefix := netip.PrefixFrom(dst, bits)
	if prefix != prefix.Masked() {
		return invalid()
	}
	linkIndex := 0
	gateway := addrs[1]
	switch gateway[1] {
	case afLink:
		if len(gateway) < 8 || 8+int(gateway[5])+int(gateway[6])+int(gateway[7]) > len(gateway) {
			return invalid()
		}
		linkIndex = int(binary.LittleEndian.Uint16(gateway[2:]))
	case afInet:
		if _, valid := ipv4Sockaddr(gateway); !valid {
			return invalid()
		}
	default:
		return invalid()
	}
	return route{destination: prefix, interfaceName: name, interfaceIndex: index, source: source, flags: flags, linkIndex: linkIndex}, true, nil
}

func ipv4Sockaddr(raw []byte) (netip.Addr, bool) {
	if len(raw) != 16 || raw[1] != afInet || raw[2] != 0 || raw[3] != 0 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(raw[4:8])), true
}
func ipv4Mask(raw []byte) (int, bool) {
	// Routing netmasks may have sa_len=0 or omit trailing zero bytes, and use
	// arbitrary radix-mask bytes in the family/port positions. The already
	// validated IPv4 destination determines the mask family, not raw[1].
	// IPv4 address mask bytes still start at offset four.
	if len(raw) == 0 {
		return 0, true
	}
	if len(raw) < 2 || len(raw) > 16 {
		return 0, false
	}
	var mask [4]byte
	if len(raw) > 4 {
		copy(mask[:], raw[4:min(len(raw), 8)])
	}
	ones, bits := net.IPMask(mask[:]).Size()
	return ones, bits == 32
}
