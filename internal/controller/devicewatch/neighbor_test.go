package devicewatch

import (
	"errors"
	"strings"
	"testing"
)

func TestParseARPFiltersInterfaceIncompleteAndMalformedRows(t *testing.T) {
	fixture := `? (192.168.1.1) at aa:bb:cc:dd:ee:ff on en0 ifscope [ethernet]
? (192.168.1.20) at (incomplete) on en0 ifscope [ethernet]
? (10.0.0.1) at 12:34:56:78:9a:bc on en1 ifscope [ethernet]
malformed row
? (192.168.1.2) at aa:bb:cc:dd:ee:01 on en0 ifscope [ethernet]
? (192.168.1.2) at aa:bb:cc:dd:ee:01 on en0 ifscope [ethernet]
`
	neighbors, err := parseARP([]byte(fixture), "en0")
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 {
		t.Fatalf("neighbors = %d, want 2: %+v", len(neighbors), neighbors)
	}
	for _, neighbor := range neighbors {
		if neighbor.InterfaceName != "en0" || !neighbor.Address.Is4() || len(neighbor.HardwareAddr) != 6 || neighbor.Method != MethodARPCache {
			t.Fatalf("unexpected neighbor: %+v", neighbor)
		}
	}
}

func TestParseNDPFiltersInterfaceAndRequiresLinkLayerAddress(t *testing.T) {
	fixture := `Neighbor                             Linklayer Address  Netif Expire    S Flags
fe80::1%en0                         aa:bb:cc:dd:ee:ff en0 23h59m S R
2001:db8:1::2                       12:34:56:78:9a:bc en0 1m R
fe80::2%en1                         22:33:44:55:66:77 en1 10m S
fe80::3%en0                         (incomplete) en0 expired N
`
	neighbors, err := parseNDP([]byte(fixture), "en0")
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 2 {
		t.Fatalf("neighbors = %d, want 2: %+v", len(neighbors), neighbors)
	}
	for _, neighbor := range neighbors {
		if neighbor.InterfaceName != "en0" || !neighbor.Address.Is6() || neighbor.Method != MethodNDPCache {
			t.Fatalf("unexpected neighbor: %+v", neighbor)
		}
	}
}

func TestParserRejectsOversizedSnapshot(t *testing.T) {
	_, err := parseARP([]byte(strings.Repeat("x", MaxSnapshotBytes+1)), "en0")
	if !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("oversized ARP error = %v", err)
	}
}
