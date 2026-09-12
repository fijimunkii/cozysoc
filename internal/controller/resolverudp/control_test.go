package resolverudp

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func controlMessage(level, kind uint32, payload []byte) []byte {
	n := 12 + len(payload)
	b := make([]byte, (n+3)&^3)
	binary.LittleEndian.PutUint32(b, uint32(n))
	binary.LittleEndian.PutUint32(b[4:], level)
	binary.LittleEndian.PutUint32(b[8:], kind)
	copy(b[12:], payload)
	return b
}
func validControl(v6 bool) []byte {
	if v6 {
		b := make([]byte, 20)
		a := netip.MustParseAddr("2001:db8::10").As16()
		copy(b, a[:])
		binary.LittleEndian.PutUint32(b[16:], 7)
		return controlMessage(41, 46, b)
	}
	link := []byte{8, 18, 7, 0, 0, 0, 0, 0}
	return append(controlMessage(0, 7, []byte{192, 0, 2, 10}), controlMessage(0, 20, link)...)
}
func TestAncillarySourceInterfaceMustBeCompleteAndUnique(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		good := validControl(v6)
		local, index, ok := parseControl(good, v6)
		if !ok || index != 7 || local.Is6() != v6 {
			t.Fatal("valid control rejected")
		}
		for i := 0; i < len(good); i++ {
			if _, _, ok := parseControl(good[:i], v6); ok {
				t.Fatal("partial control accepted")
			}
		}
		if _, _, ok := parseControl(append(append([]byte(nil), good...), good...), v6); ok {
			t.Fatal("duplicate accepted")
		}
		if _, _, ok := parseControl(good, !v6); ok {
			t.Fatal("wrong family accepted")
		}
		bad := append([]byte(nil), good...)
		binary.LittleEndian.PutUint32(bad[8:], 99)
		if _, _, ok := parseControl(bad, v6); ok {
			t.Fatal("unknown control accepted")
		}
	}
}
func FuzzAncillaryControl(f *testing.F) {
	f.Add(validControl(false), false)
	f.Add(validControl(true), true)
	f.Add([]byte{}, false)
	f.Fuzz(func(t *testing.T, b []byte, v6 bool) {
		local, index, ok := parseControl(b, v6)
		if ok && (!local.IsValid() || local.Is6() != v6 || index < 1 || index > 2147483647) {
			t.Fatal("invalid control evidence")
		}
	})
}
