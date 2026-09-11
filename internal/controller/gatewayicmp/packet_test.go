package gatewayicmp

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func controlMessage(kind uint32, payload []byte) []byte {
	n := 12 + len(payload)
	b := make([]byte, (n+3)&^3)
	binary.LittleEndian.PutUint32(b, uint32(n))
	binary.LittleEndian.PutUint32(b[8:], kind)
	copy(b[12:], payload)
	return b
}
func goodControl() []byte {
	link := make([]byte, 20)
	link[0], link[1], link[2] = 20, 18, 7
	return append(controlMessage(7, []byte{192, 168, 50, 23}), controlMessage(20, link)...)
}
func TestStrictEchoMatching(t *testing.T) {
	r := requestAt(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	nonce := [32]byte{1, 2, 3}
	p := echoRequest(1234, 1, nonce)
	p[0], p[2], p[3] = 0, 0, 0
	binary.BigEndian.PutUint16(p[2:], checksum(p[:]))
	base := datagram{data: p[:], peer: r.Plan.Target, local: r.Source, index: 7}
	if !matchesReply(base, r, 1234, 1, nonce) {
		t.Fatal("valid synthetic reply rejected")
	}
	for name, edit := range map[string]func(*datagram){
		"peer":      func(d *datagram) { d.peer = netip.MustParseAddr("192.168.50.2") },
		"local":     func(d *datagram) { d.local = netip.MustParseAddr("192.168.50.24") },
		"interface": func(d *datagram) { d.index++ }, "missing metadata": func(d *datagram) { d.index = 0 },
		"truncation": func(d *datagram) { d.truncated = true },
		"extra":      func(d *datagram) { d.data = append(d.data, 0) }, "short": func(d *datagram) { d.data = d.data[:39] },
		"request": func(d *datagram) { d.data[0] = 8 }, "code": func(d *datagram) { d.data[1] = 1 },
		"corrupt": func(d *datagram) { d.data[2]++ },
		"id":      func(d *datagram) { d.data[4]++ }, "seq": func(d *datagram) { d.data[7]++ },
		"nonce": func(d *datagram) { d.data[8]++ },
	} {
		t.Run(name, func(t *testing.T) {
			d := base
			d.data = bytes.Clone(base.data)
			edit(&d)
			// Valid checksum must not rescue a wrong identifier/nonce/type/length.
			if name != "corrupt" && len(d.data) >= 4 {
				d.data[2], d.data[3] = 0, 0
				binary.BigEndian.PutUint16(d.data[2:], checksum(d.data))
			}
			if matchesReply(d, r, 1234, 1, nonce) {
				t.Fatal("foreign or invalid echo accepted")
			}
		})
	}
	for n := 0; n < 40; n++ {
		d := base
		d.data = d.data[:n]
		if matchesReply(d, r, 1234, 1, nonce) {
			t.Fatal("short reply")
		}
	}
	if checksum([]byte{1, 2, 3}) != 0xfbfd || checksum(nil) != 0xffff {
		t.Fatal("incorrect one's-complement checksum")
	}
}

func TestAncillaryRequiresOneDestinationAndOneInterface(t *testing.T) {
	local, index, ok := parseControl(goodControl())
	if !ok || local.String() != "192.168.50.23" || index != 7 {
		t.Fatal(local, index, ok)
	}
	for name, edit := range map[string]func([]byte) []byte{
		"duplicate destination": func(b []byte) []byte { return append(b, b[:16]...) },
		"duplicate interface":   func(b []byte) []byte { return append(b, b[16:]...) },
		"missing interface":     func(b []byte) []byte { return b[:16] },
		"missing destination":   func(b []byte) []byte { return b[16:] },
		"unknown":               func(b []byte) []byte { b[8] = 99; return b },
		"wrong level":           func(b []byte) []byte { b[4] = 1; return b },
		"zero length":           func(b []byte) []byte { b[0] = 0; return b },
		"short header":          func(b []byte) []byte { b[0] = 11; return b },
		"huge length":           func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 0xffffffff); return b },
		"wrong sockaddr len":    func(b []byte) []byte { b[28]++; return b },
		"wrong sockaddr family": func(b []byte) []byte { b[29] = 2; return b },
		"zero index":            func(b []byte) []byte { b[30] = 0; return b },
		"overflow link bytes":   func(b []byte) []byte { b[33] = 255; return b },
		"extra":                 func(b []byte) []byte { return append(b, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := parseControl(edit(goodControl())); ok {
				t.Fatal("malformed controls accepted")
			}
		})
	}
	for n := 0; n < len(goodControl()); n++ {
		if _, _, ok := parseControl(goodControl()[:n]); ok {
			t.Fatal("truncated controls accepted")
		}
	}
	if _, _, ok := parseControl(make([]byte, maxControlBytes+1)); ok {
		t.Fatal("oversized control accepted")
	}
}

func FuzzEchoAndAncillary(f *testing.F) {
	f.Add([]byte{}, goodControl())
	p := echoRequest(1, 1, [32]byte{1})
	f.Add(p[:], []byte{})
	f.Fuzz(func(t *testing.T, p, control []byte) {
		local, index, ok := parseControl(control)
		if ok && (!local.Is4() || index < 1) {
			t.Fatal("invalid ancillary success")
		}
		r := requestAt(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
		d := datagram{data: p, peer: r.Plan.Target, local: local, index: index}
		if matchesReply(d, r, 1, 1, [32]byte{1}) && (len(p) != 40 || p[0] != 0 || p[1] != 0 || checksum(p) != 0) {
			t.Fatal("invalid echo success")
		}
	})
}
