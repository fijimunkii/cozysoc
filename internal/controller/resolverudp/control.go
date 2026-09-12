package resolverudp

import (
	"encoding/binary"
	"net/netip"
)

// Darwin cmsghdr is 12 bytes with four-byte alignment on both supported targets.
// Decode IPv4 IP_RECVDSTADDR/IP_RECVIF or IPv6 IPV6_PKTINFO; reject duplicates, unknown
// controls, truncation and malformed sockaddr_dl. Keep no link-layer address data.
// Constants/layout are checked against the platform ABI in the Darwin adapter.
func parseControl(data []byte, v6 bool) (local netip.Addr, index int, ok bool) {
	if len(data) == 0 || len(data) > maxControlBytes {
		return netip.Addr{}, 0, false
	}
	for len(data) != 0 {
		if len(data) < 12 {
			return netip.Addr{}, 0, false
		}
		n := int(binary.LittleEndian.Uint32(data))
		if n < 12 || n > len(data) {
			return netip.Addr{}, 0, false
		}
		payload := data[12:n]
		level := binary.LittleEndian.Uint32(data[4:])
		if v6 {
			if level != 41 || binary.LittleEndian.Uint32(data[8:]) != 46 || local.IsValid() || len(payload) != 20 {
				return netip.Addr{}, 0, false
			}
			local = netip.AddrFrom16([16]byte(payload[:16]))
			index = int(binary.LittleEndian.Uint32(payload[16:]))
		} else {
			if level != 0 {
				return netip.Addr{}, 0, false
			}
			switch binary.LittleEndian.Uint32(data[8:]) {
			case 7: // IP_RECVDSTADDR
				if local.IsValid() || len(payload) != 4 {
					return netip.Addr{}, 0, false
				}
				local = netip.AddrFrom4([4]byte(payload))
			case 20: // IP_RECVIF: sockaddr_dl
				if index != 0 || len(payload) < 8 || int(payload[0]) != len(payload) || payload[1] != 18 ||
					8+int(payload[5])+int(payload[6])+int(payload[7]) > len(payload) {
					return netip.Addr{}, 0, false
				}
				index = int(binary.LittleEndian.Uint16(payload[2:]))
				if index == 0 {
					return netip.Addr{}, 0, false
				}
			default:
				return netip.Addr{}, 0, false
			}
		}
		step := (n + 3) &^ 3
		if step > len(data) {
			return netip.Addr{}, 0, false
		}
		data = data[step:]
	}
	return local, index, local.IsValid() && local.Is6() == v6 && !local.Is4In6() && index > 0 && index <= 2147483647
}
