package checkroute

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/net/route"
)

func packet(t testing.TB, v6 bool) []byte {
	t.Helper()
	dst := route.Addr(&route.Inet4Addr{IP: [4]byte{192, 0, 2, 0}})
	src := route.Addr(&route.Inet4Addr{IP: [4]byte{192, 0, 2, 10}})
	mask := route.Addr(&route.Inet4Addr{IP: [4]byte{255, 255, 255, 0}})
	if v6 {
		dst = &route.Inet6Addr{IP: netip.MustParseAddr("2001:db8:1::").As16()}
		src = &route.Inet6Addr{IP: netip.MustParseAddr("2001:db8:1::10").As16()}
		mask = &route.Inet6Addr{IP: netip.MustParseAddr("ffff:ffff:ffff:ffff::").As16()}
	}
	b, e := (&route.RouteMessage{Version: syscall.RTM_VERSION, Type: syscall.RTM_GET, Flags: syscall.RTF_UP | syscall.RTF_DONE, Index: 7, ID: 123, Seq: 456, Addrs: []route.Addr{syscall.RTAX_DST: dst, syscall.RTAX_GATEWAY: &route.LinkAddr{Index: 7}, syscall.RTAX_NETMASK: mask, syscall.RTAX_IFP: &route.LinkAddr{Index: 7, Name: "fixture0"}, syscall.RTAX_IFA: src}}).Marshal()
	if e != nil {
		t.Fatal(e)
	}
	// Marshal pads Darwin requests for an old kernel workaround. Synthetic replies
	// carry only the kernel's actual fields and correct message length.
	b = b[:len(b)-1024]
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	return b
}
func fieldOffset(b []byte, slot int) int {
	offset := routeHeaderBytes
	mask := binary.LittleEndian.Uint32(b[12:])
	for i := 0; i < slot; i++ {
		if mask&(1<<i) != 0 {
			n := (int(b[offset]) + 3) &^ 3
			if n == 0 {
				n = 4
			}
			offset += n
		}
	}
	return offset
}
func TestDarwinDecoderFamiliesMasksAndErrno(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		b := packet(t, v6)
		r, matched, e := parseReply(b, 123, 456)
		if e != nil || !matched || r.source.Is6() != v6 {
			t.Fatalf("v6=%t: %+v %v", v6, r, e)
		}
		// rt_use is not errno. A nonzero use counter must not reject a valid route.
		binary.LittleEndian.PutUint32(b[28:], 13)
		if _, _, e := parseReply(b, 123, 456); e != nil {
			t.Fatal("rt_use treated as errno", e)
		}
		binary.LittleEndian.PutUint32(b[24:], uint32(syscall.EACCES))
		if _, _, e := parseReply(b, 123, 456); !errors.Is(e, ErrPermission) {
			t.Fatal("errno ignored", e)
		}
		for _, family := range []byte{0, 2, 18, 30, 255} {
			b := packet(t, v6)
			off := fieldOffset(b, syscall.RTAX_NETMASK)
			b[off+1] = family
			if _, _, e := parseReply(b, 123, 456); e != nil {
				t.Fatalf("radix family=%d v6=%t: %v", family, v6, e)
			}
		}
	}
	// Observed Darwin compact /24 shape: family and port bytes can contain mask bits.
	b := packet(t, false)
	off := fieldOffset(b, syscall.RTAX_NETMASK)
	compact := []byte{7, 255, 255, 255, 255, 255, 255, 0}
	b = append(append(append([]byte(nil), b[:off]...), compact...), b[off+16:]...)
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	if r, _, e := parseReply(b, 123, 456); e != nil || r.destination.Bits() != 24 {
		t.Fatalf("compact mask: %+v %v", r, e)
	}
	b[off+6] = 127
	if _, _, e := parseReply(b, 123, 456); e == nil {
		t.Fatal("accepted noncontiguous mask")
	}
}
func TestDarwinMalformedAndForeignReplies(t *testing.T) {
	good := packet(t, false)
	for i := 0; i < len(good); i++ {
		if _, _, e := parseReply(good[:i], 123, 456); e == nil {
			t.Fatalf("accepted prefix %d", i)
		}
	}
	for _, flag := range []uint32{syscall.RTF_REJECT, syscall.RTF_BLACKHOLE, syscall.RTF_LOCAL, syscall.RTF_MULTICAST, syscall.RTF_BROADCAST, syscall.RTF_DYNAMIC, 0x80000000} {
		b := packet(t, false)
		binary.LittleEndian.PutUint32(b[8:], binary.LittleEndian.Uint32(b[8:])|flag)
		if _, _, e := parseReply(b, 123, 456); !errors.Is(e, ErrMismatch) {
			t.Fatalf("accepted flags %#x", flag)
		}
	}
	if _, matched, e := parseReply(good, 124, 456); e != nil || matched {
		t.Fatal("foreign pid matched")
	}
	if _, matched, e := parseReply(good, 123, 457); e != nil || matched {
		t.Fatal("foreign sequence matched")
	}
	for _, mut := range []func([]byte) []byte{func(b []byte) []byte { b = append(b, 0); binary.LittleEndian.PutUint16(b, uint16(len(b))); return b }, func(b []byte) []byte { b[routeHeaderBytes] = 255; return b }, func(b []byte) []byte { b[12] = 0; return b }, func(b []byte) []byte { b[2]++; return b }} {
		if _, _, e := parseReply(mut(append([]byte(nil), good...)), 123, 456); e == nil {
			t.Fatal("accepted malformed message")
		}
	}
}
func TestDarwinRequestIsUnscopedReadOnly(t *testing.T) {
	for _, target := range []string{"192.0.2.53", "2001:db8::53"} {
		b, e := getRequest(netip.MustParseAddr(target), 123, 456)
		if e != nil {
			t.Fatal(e)
		}
		if b[3] != syscall.RTM_GET || binary.LittleEndian.Uint16(b[4:]) != 0 || binary.LittleEndian.Uint32(b[8:]) != 0 || binary.LittleEndian.Uint32(b[12:]) != 1<<syscall.RTAX_DST|1<<syscall.RTAX_IFP {
			t.Fatal("request changes or forces routing")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := lookupSystem(ctx, netip.MustParseAddr("192.0.2.53")); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func FuzzDarwinRouteReply(f *testing.F) {
	f.Add(packet(f, false))
	f.Add(packet(f, true))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		r, matched, e := parseReply(b, 123, 456)
		if e == nil && matched && (!r.destination.IsValid() || !r.source.IsValid() || r.index < 1) {
			t.Fatal("invalid evidence")
		}
	})
}

func TestDarwinRoutedIPv6GatewayScope(t *testing.T) {
	b := packet(t, true)
	off := fieldOffset(b, syscall.RTAX_GATEWAY)
	gateway := make([]byte, 28)
	gateway[0], gateway[1] = 28, syscall.AF_INET6
	ip := netip.MustParseAddr("fe80::1").As16()
	copy(gateway[8:], ip[:])
	binary.BigEndian.PutUint16(gateway[10:12], 7)
	b = append(append(append([]byte(nil), b[:off]...), gateway...), b[off+8:]...)
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	binary.LittleEndian.PutUint32(b[8:], syscall.RTF_UP|syscall.RTF_DONE|syscall.RTF_GATEWAY)
	r, matched, e := parseReply(b, 123, 456)
	if e != nil || !matched || r.gateway != netip.MustParseAddr("fe80::1") || r.gatewayZone != 7 {
		t.Fatalf("gateway scope: %+v %v", r, e)
	}
	binary.LittleEndian.PutUint32(b[off+24:off+28], 8)
	if _, _, e := parseReply(b, 123, 456); e == nil {
		t.Fatal("accepted conflicting scope encodings")
	}
}
