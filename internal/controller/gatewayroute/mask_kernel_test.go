package gatewayroute

import "testing"

func TestRouteMaskUsesDestinationFamilyNotRadixMaskBytes(t *testing.T) {
	// Synthetic variants of the compressed /24 shape observed in the isolated
	// macOS 15/26 feth lab. The sockaddr's family/port positions are mask bits,
	// not an address family. parseReply already requires an IPv4 destination.
	for _, value := range []byte{0, afInet, 30, 255} {
		mask := []byte{7, value, 255, 255, 255, 255, 255}
		r, matched, err := parseReply(reply(flagUp|flagDone, mask), 123, 456)
		if err != nil || !matched || r.destination.String() != "192.168.50.0/24" {
			t.Fatalf("mask prefix byte %d: %+v %v", value, r, err)
		}
		mask[6] = 127
		if _, _, err := parseReply(reply(flagUp|flagDone, mask), 123, 456); err == nil {
			t.Fatal("noncontiguous address mask accepted")
		}
	}
}
