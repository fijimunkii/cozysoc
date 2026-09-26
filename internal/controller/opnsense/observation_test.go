package opnsense

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestRouterNeighborProjectionPreservesScopeAndProvenance(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	snapshot := Snapshot{Status: Status{Version: SupportedVersion}, IPv4Total: 3, IPv6Total: 1, Neighbors: []Neighbor{
		{IP: netip.MustParseAddr("192.168.1.10"), MAC: "02:00:00:00:00:10", Interface: "igb0", Family: "ipv4"},
		{IP: netip.MustParseAddr("192.168.1.10"), MAC: "02:00:00:00:00:10", Interface: "igb0", Family: "ipv4"},
		{IP: netip.MustParseAddr("192.168.2.10"), MAC: "02:00:00:00:00:11", Interface: "igb1", Family: "ipv4"},
		{IP: netip.MustParseAddr("fd00:1::10"), MAC: "02:00:00:00:00:12", Interface: "igb0", Family: "ipv6"},
	}}
	prefixes := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("fd00:1::/64")}
	observations, stats, err := BuildObservations(snapshot, "scope.home", "sensor.router", prefixes, now)
	if err != nil || len(observations) != 2 || stats.Selected != 2 || stats.OutsideScope != 1 || stats.Duplicate != 1 {
		t.Fatalf("projection: %d %+v %v", len(observations), stats, err)
	}
	for _, observation := range observations {
		if observation.Kind != NeighborObservationKind || observation.Attribution != "opnsense:neighbor-table;device-identity-unverified" || observation.ScopeID != "scope.home" {
			t.Fatalf("overstated provenance: %+v", observation)
		}
		encoded, _ := json.Marshal(observation)
		if strings.Contains(string(encoded), "192.168.2.10") || strings.Contains(string(encoded), "router-name") {
			t.Fatalf("out-of-scope or unrelated data leaked: %s", encoded)
		}
	}
}

func TestRouterNeighborProjectionRejectsMalformedTypedInput(t *testing.T) {
	now := time.Now().UTC()
	for _, neighbor := range []Neighbor{
		{IP: netip.MustParseAddr("192.168.1.10"), MAC: "ff:ff:ff:ff:ff:ff", Interface: "igb0", Family: "ipv4"},
		{IP: netip.MustParseAddr("192.168.1.10"), MAC: "00:00:00:00:00:00", Interface: "igb0", Family: "ipv4"},
		{IP: netip.MustParseAddr("192.168.1.10"), MAC: "02:00:00:00:00:10", Interface: "bad\nname", Family: "ipv4"},
		{IP: netip.MustParseAddr("fd00:1::10"), MAC: "02:00:00:00:00:10", Interface: "igb0", Family: "ipv4"},
	} {
		_, _, err := BuildObservations(Snapshot{IPv4Total: 1, Neighbors: []Neighbor{neighbor}}, "scope.home", "sensor.router", []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, now)
		if err == nil {
			t.Fatalf("accepted invalid neighbor %+v", neighbor)
		}
	}
}
