package gatewayroute

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// Synthetic Darwin ABI datagrams, not captured route tables or household data.
func inet(address string) []byte {
	b := make([]byte, 16)
	b[0], b[1] = 16, afInet
	a := netip.MustParseAddr(address).As4()
	copy(b[4:], a[:])
	return b
}
func link(name string) []byte {
	b := make([]byte, 8+len(name))
	b[0], b[1] = byte(len(b)), afLink
	binary.LittleEndian.PutUint16(b[2:], 7)
	b[5] = byte(len(name))
	copy(b[8:], name)
	return b
}
func reply(flags uint32, mask []byte) []byte {
	b := make([]byte, routeHeaderBytes)
	b[2], b[3] = routeVersion, routeGet
	binary.LittleEndian.PutUint16(b[4:], 7)
	binary.LittleEndian.PutUint32(b[8:], flags)
	bits := uint32(1<<0 | 1<<1 | 1<<4 | 1<<5)
	dst := "192.168.50.1"
	if flags&flagHost == 0 {
		dst = "192.168.50.0"
	}
	addresses := [][]byte{inet(dst), link("")}
	if mask != nil {
		bits |= 1 << 2
		addresses = append(addresses, mask)
	}
	addresses = append(addresses, link("en0"), inet("192.168.50.23"))
	binary.LittleEndian.PutUint32(b[12:], bits)
	binary.LittleEndian.PutUint32(b[16:], 123)
	binary.LittleEndian.PutUint32(b[20:], 456)
	for _, a := range addresses {
		b = append(b, a...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	return b
}
func goodReply() []byte { return reply(flagUp|flagDone|flagHost, nil) }

func TestRequestIsOnlyUnscopedNumericRTMGet(t *testing.T) {
	target := netip.MustParseAddr("192.168.50.1")
	b, err := getRequest(target, 123, 456)
	if err != nil || len(b) != 128 || b[3] != routeGet || binary.LittleEndian.Uint32(b[12:]) != 0x11 || binary.LittleEndian.Uint16(b[4:]) != 0 || binary.LittleEndian.Uint32(b[8:]) != 0 {
		t.Fatalf("unsafe request %x %v", b, err)
	}
	for _, address := range []string{"8.8.8.8", "127.0.0.1", "::ffff:192.168.50.1", "fe80::1"} {
		if _, err := getRequest(netip.MustParseAddr(address), 123, 456); err == nil {
			t.Fatal("unscoped destination accepted")
		}
	}
	for _, pair := range [][2]uint32{{0, 456}, {123, 0}} {
		if _, err := getRequest(target, pair[0], pair[1]); err == nil {
			t.Fatal("missing reply correlation")
		}
	}
}
func TestReplyPreservesOnlyRouteAndIFAEvidence(t *testing.T) {
	for _, tc := range []struct {
		data   []byte
		prefix string
	}{
		{goodReply(), "192.168.50.1/32"},
		{reply(flagUp|flagDone, []byte{7, 0, 0, 0, 255, 255, 255}), "192.168.50.0/24"},
	} {
		r, matched, err := parseReply(tc.data, 123, 456)
		if err != nil || !matched || r.destination.String() != tc.prefix || r.source.String() != "192.168.50.23" || r.interfaceName != "en0" || r.interfaceIndex != 7 || r.linkIndex != 7 {
			t.Fatalf("route=%+v match=%v error=%v", r, matched, err)
		}
	}
	for _, pair := range [][2]uint32{{124, 456}, {123, 457}} {
		_, matched, err := parseReply(goodReply(), pair[0], pair[1])
		if err != nil || matched {
			t.Fatal("foreign reply matched")
		}
	}
	b := goodReply()
	b[3] = 12
	if _, matched, err := parseReply(b, 123, 456); err != nil || matched {
		t.Fatal("notification matched query")
	}
}
func TestReplyRejectsMalformedDatagrams(t *testing.T) {
	edits := map[string]func([]byte) []byte{
		"length": func(b []byte) []byte { b[0]--; return b }, "version": func(b []byte) []byte { b[2]++; return b },
		"missing done":      func(b []byte) []byte { binary.LittleEndian.PutUint32(b[8:], flagUp|flagHost); return b },
		"unknown addresses": func(b []byte) []byte { b[15] = 1; return b }, "missing IFA": func(b []byte) []byte { b[12] &= ^byte(1 << 5); return b },
		"oversized sockaddr": func(b []byte) []byte { b[92] = 255; return b }, "short sockaddr": func(b []byte) []byte { b[92] = 1; return b },
		"wrong address family": func(b []byte) []byte { b[93] = 30; return b }, "port": func(b []byte) []byte { b[94] = 80; return b },
		"wrong header index": func(b []byte) []byte { b[4] = 8; return b },
		"IFP bounds":         func(b []byte) []byte { b[116+5] = 64; return b },
		"hostile IFP":        func(b []byte) []byte { b[124] = '<'; return b },
		"wrong IFP family":   func(b []byte) []byte { b[117] = afInet; return b },
		"gateway family":     func(b []byte) []byte { b[109] = 30; return b },
		"gateway bounds":     func(b []byte) []byte { b[113] = 255; return b },
		"extra byte":         func(b []byte) []byte { b = append(b, 0); binary.LittleEndian.PutUint16(b, uint16(len(b))); return b },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			if r, _, err := parseReply(edit(goodReply()), 123, 456); err == nil || r != (route{}) {
				t.Fatalf("invalid data accepted: %+v %v", r, err)
			}
		})
	}
	for n := 0; n < len(goodReply()); n++ {
		b := goodReply()[:n]
		if n >= 2 {
			binary.LittleEndian.PutUint16(b, uint16(n))
		}
		if _, _, err := parseReply(b, 123, 456); err == nil {
			t.Fatalf("truncation %d accepted", n)
		}
	}
	if _, _, err := parseReply(make([]byte, 4097), 123, 456); err == nil {
		t.Fatal("oversized accepted")
	}
	for _, mask := range [][]byte{{6, 0, 0, 0, 255, 127}, {16, 30, 0, 0, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0}} {
		if _, _, err := parseReply(reply(flagUp|flagDone, mask), 123, 456); err == nil {
			t.Fatal("invalid mask accepted")
		}
	}
	for code, want := range map[uint32]error{1: ErrPermission, 13: ErrPermission, 3: ErrNoRoute, 51: ErrNoRoute, 65: ErrNoRoute, 22: ErrUnavailable} {
		b := goodReply()
		binary.LittleEndian.PutUint32(b[24:], code)
		if _, matched, err := parseReply(b, 123, 456); !matched || err != want {
			t.Fatalf("errno %d: %v", code, err)
		}
	}
}
func TestZeroAndCompressedMasks(t *testing.T) {
	for _, tc := range []struct {
		raw   []byte
		bits  int
		valid bool
	}{{nil, 0, true}, {[]byte{2, 0}, 0, true}, {[]byte{7, 0, 0, 0, 255, 255, 255}, 24, true}, {[]byte{6, 0, 0, 0, 255, 0}, 8, true}, {[]byte{6, 0, 0, 0, 255, 127}, 0, false}, {[]byte{1}, 0, false}} {
		bits, valid := ipv4Mask(tc.raw)
		if bits != tc.bits || valid != tc.valid {
			t.Fatalf("mask %x: %d %v", tc.raw, bits, valid)
		}
	}
}
func FuzzDarwinRouteReply(f *testing.F) {
	f.Add(goodReply())
	f.Add([]byte{})
	f.Add(reply(flagUp|flagDone, []byte{7, 0, 0, 0, 255, 255, 255}))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, matched, err := parseReply(b, 123, 456)
		if err == nil && matched {
			if !r.source.Is4() || !r.destination.IsValid() || r.interfaceIndex < 1 {
				t.Fatal("invalid parsed success")
			}
		}
	})
}
