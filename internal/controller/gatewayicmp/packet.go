package gatewayicmp

import (
	"bytes"
	"encoding/binary"
	"net/netip"
)

func checksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func echoRequest(id, seq uint16, nonce [32]byte) [packetBytes]byte {
	var packet [packetBytes]byte
	packet[0] = 8 // ICMPv4 echo request, code zero. No user-controlled payload.
	binary.BigEndian.PutUint16(packet[4:], id)
	binary.BigEndian.PutUint16(packet[6:], seq)
	copy(packet[8:], nonce[:])
	binary.BigEndian.PutUint16(packet[2:], checksum(packet[:]))
	return packet
}

func matchesReply(d datagram, r Request, id, seq uint16, nonce [32]byte) bool {
	// IP_STRIPHDR is required by the adapter: never guess between packet formats.
	// Kernel ancillary destination/interface and recvmsg peer must all agree.
	p := d.data
	return !d.truncated && d.peer == r.Plan.Target && d.local == r.Source && d.index == r.Plan.Binding.InterfaceIndex &&
		len(p) == packetBytes && p[0] == 0 && p[1] == 0 && checksum(p) == 0 &&
		binary.BigEndian.Uint16(p[4:]) == id && binary.BigEndian.Uint16(p[6:]) == seq && bytes.Equal(p[8:], nonce[:])
}

// Darwin cmsghdr is 12 bytes with four-byte alignment on both supported targets.
// Decode only required IP_RECVDSTADDR and IP_RECVIF; reject duplicates, unknown
// controls, truncation and malformed sockaddr_dl. Keep no link-layer address data.
// Constants/layout are checked against the platform ABI in the Darwin adapter.
func parseControl(data []byte) (local netip.Addr, index int, ok bool) {
	if len(data) == 0 || len(data) > maxControlBytes {
		return netip.Addr{}, 0, false
	}
	for len(data) != 0 {
		if len(data) < 12 {
			return netip.Addr{}, 0, false
		}
		n := int(binary.LittleEndian.Uint32(data))
		if n < 12 || n > len(data) || binary.LittleEndian.Uint32(data[4:]) != 0 {
			return netip.Addr{}, 0, false
		}
		payload := data[12:n]
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
		step := (n + 3) &^ 3
		if step > len(data) {
			return netip.Addr{}, 0, false
		}
		data = data[step:]
	}
	return local, index, local.Is4() && index > 0
}
