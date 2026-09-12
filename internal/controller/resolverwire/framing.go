package resolverwire

import (
	"encoding/binary"

	"golang.org/x/net/dns/dnsmessage"
)

// dnsmessage handles name expansion and resource decoding. This small envelope
// pass additionally requires exact message/RDATA boundaries and backward pointers
// to known name-label starts, which its general parser does not enforce. It never
// expands names or interprets records. All work is bounded by 512 bytes/32 RRs.
func validateFraming(packet []byte, total int) error {
	names := make(map[int]bool)
	offset, err := nameEnd(packet, 12, len(packet), names)
	if err != nil {
		return err
	}
	offset += 4
	if offset > len(packet) {
		return ErrResponse
	}
	for i := 0; i < total; i++ {
		offset, err = nameEnd(packet, offset, len(packet), names)
		if err != nil || offset+10 > len(packet) {
			return ErrResponse
		}
		kind := dnsmessage.Type(binary.BigEndian.Uint16(packet[offset : offset+2]))
		size := int(binary.BigEndian.Uint16(packet[offset+8 : offset+10]))
		offset += 10
		end := offset + size
		if end > len(packet) {
			return ErrResponse
		}
		switch kind {
		case dnsmessage.TypeA:
			if size != 4 {
				return ErrResponse
			}
		case dnsmessage.TypeAAAA:
			if size != 16 {
				return ErrResponse
			}
		case dnsmessage.TypeCNAME, dnsmessage.TypeNS:
			after, e := nameEnd(packet, offset, end, names)
			if e != nil || after != end {
				return ErrResponse
			}
		case dnsmessage.TypeSOA:
			after, e := nameEnd(packet, offset, end, names)
			if e != nil {
				return ErrResponse
			}
			after, e = nameEnd(packet, after, end, names)
			if e != nil || after+20 != end {
				return ErrResponse
			}
		case dnsmessage.TypeOPT:
			return ErrResponse // No EDNS request was sent.
		}
		offset = end
	}
	if offset != len(packet) {
		return ErrResponse
	}
	return nil
}

// Walk only the encoded portion of a name; dnsmessage validates expansion,
// including pointer loops and the expanded-name length. The RDATA end prevents
// a short record from borrowing bytes from the next record.
func nameEnd(packet []byte, offset, end int, names map[int]bool) (int, error) {
	for offset < end {
		start := offset
		size := int(packet[offset])
		offset++
		switch size & 0xc0 {
		case 0:
			names[start] = true
			if size == 0 {
				return offset, nil
			}
			if offset+size > end {
				return 0, ErrResponse
			}
			offset += size
		case 0xc0:
			if offset >= end {
				return 0, ErrResponse
			}
			target := (size&0x3f)<<8 | int(packet[offset])
			if target >= start || !names[target] {
				return 0, ErrResponse
			}
			names[start] = true
			return offset + 1, nil
		default:
			return 0, ErrResponse
		}
	}
	return 0, ErrResponse
}
