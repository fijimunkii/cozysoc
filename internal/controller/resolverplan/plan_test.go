package resolverplan

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var at = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func fixture() (Binding, Configuration) {
	return Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "en0", InterfaceIndex: 4}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")},
		Configuration{Selection: nq.ResolverSelection{ID: "dns1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Endpoint: netip.MustParseAddrPort("192.0.2.53:53"), Name: "Test.Example.", DestinationScope: EnrolledPrefix}
}
func mustPlan(t testing.TB, b Binding, c Configuration, now time.Time) Plan {
	t.Helper()
	p, e := New(b, c, now)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestDisclosureAndImmutability(t *testing.T) {
	b, c := fixture()
	b.Prefixes = append(b.Prefixes, "198.51.100.0/24")
	p := mustPlan(t, b, c, at)
	d := p.Disclosure()
	if d.Configuration.Name != "test.example." || d.Configuration.Endpoint != c.Endpoint || d.OutsideEnrolledPrefixes || !d.MayForwardUpstream || d.Profile != Profile {
		t.Fatalf("unexpected disclosure %+v", d)
	}
	if d.Budget.MaxSendCalls != 1 || d.Budget.MaxRequestBytes != 30 || d.Budget.MaxReplyBytes != 512 || d.Budget.MaxReceivedDatagrams != 16 || d.Budget.MaxReceiveCalls != 512 || d.Budget.ExchangeTimeout != 2*time.Second || d.Budget.TotalTimeout != 5*time.Second || d.Budget.MaxConcurrentRuns != 1 || d.Budget.MinRunInterval != time.Minute {
		t.Fatalf("unexpected budget %+v", d.Budget)
	}
	b.Prefixes[0] = "203.0.113.0/24"
	d.Binding.Prefixes[0] = "203.0.113.0/24"
	d.Configuration.Name = "changed.example."
	d.Budget.MaxSendCalls = 100
	if fresh := p.Disclosure(); fresh.Binding.Prefixes[0] != "192.0.2.0/24" || fresh.Configuration.Name != "test.example." || fresh.Budget.MaxSendCalls != 1 {
		t.Fatal("caller mutated plan")
	}
	for _, text := range []string{fmt.Sprint(p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p)} {
		if strings.Contains(text, "example") || strings.Contains(text, "192.") {
			t.Fatal("plan leaked in formatting")
		}
	}
	data, e := json.Marshal(p)
	if e != nil || string(data) != `"[resolver review plan]"` {
		t.Fatalf("JSON: %s %v", data, e)
	}
	var decoded Plan
	if e := json.Unmarshal([]byte(`{"review":{"Profile":"resolver-udp-v1"}}`), &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded.Current(at) || decoded.SameSelection(p) {
		t.Fatal("decoded plan acquired validity")
	}
}

func TestExplicitDestinationAndFamilies(t *testing.T) {
	b, c := fixture()
	c.Endpoint = netip.MustParseAddrPort("198.51.100.53:53")
	if _, e := New(b, c, at); e != ErrBinding {
		t.Fatal("enrolled policy accepted outside destination")
	}
	c.DestinationScope = ExactEndpoint
	p := mustPlan(t, b, c, at)
	if !p.Disclosure().OutsideEnrolledPrefixes {
		t.Fatal("outside destination undisclosed")
	}
	c.Endpoint = netip.MustParseAddrPort("192.0.2.53:53")
	if mustPlan(t, b, c, at).Disclosure().OutsideEnrolledPrefixes {
		t.Fatal("local endpoint mislabeled outside")
	}
	c.Selection.QueryType = nq.DNSQueryAAAA
	mustPlan(t, b, c, at) // Query family is independent.
	b.Source = netip.MustParseAddr("2001:db8:1::10")
	b.Prefixes = []string{"2001:db8:1::/64"}
	c.Endpoint = netip.MustParseAddrPort("[2001:db8:1::53]:53")
	c.Selection.Family = nq.FamilyIPv6
	c.Selection.QueryType = nq.DNSQueryA
	c.DestinationScope = EnrolledPrefix
	mustPlan(t, b, c, at)
	c.Endpoint = netip.MustParseAddrPort("[2001:db8:2::53]:53")
	if _, e := New(b, c, at); e != ErrBinding {
		t.Fatal("IPv6 scope escaped")
	}
	c.DestinationScope = ExactEndpoint
	mustPlan(t, b, c, at)
}

func TestInvalidConfigurationAndBinding(t *testing.T) {
	mutations := map[string]func(*Binding, *Configuration){
		"missing policy":    func(b *Binding, c *Configuration) { c.DestinationScope = "" },
		"unknown policy":    func(b *Binding, c *Configuration) { c.DestinationScope = "anywhere" },
		"TCP":               func(b *Binding, c *Configuration) { c.Selection.Transport = nq.DNSTCP },
		"missing reference": func(b *Binding, c *Configuration) { c.Selection.QueryID = "" },
		"bad reference":     func(b *Binding, c *Configuration) { c.Selection.ResolverID = "private.example/path" },
		"family mismatch":   func(b *Binding, c *Configuration) { c.Selection.Family = nq.FamilyIPv6 },
		"bad expectation":   func(b *Binding, c *Configuration) { c.Selection.Expect = "success" },
		"relative name":     func(b *Binding, c *Configuration) { c.Name = "test.example" },
		"hostname URL":      func(b *Binding, c *Configuration) { c.Name = "https://test.example." },
		"missing endpoint":  func(b *Binding, c *Configuration) { c.Endpoint = netip.AddrPort{} },
		"other port":        func(b *Binding, c *Configuration) { c.Endpoint = netip.MustParseAddrPort("192.0.2.53:5353") },
		"loopback endpoint": func(b *Binding, c *Configuration) {
			c.Endpoint = netip.MustParseAddrPort("127.0.0.1:53")
			c.DestinationScope = ExactEndpoint
		},
		"class E endpoint": func(b *Binding, c *Configuration) {
			c.Endpoint = netip.MustParseAddrPort("240.0.0.1:53")
			c.DestinationScope = ExactEndpoint
		},
		"zero net endpoint": func(b *Binding, c *Configuration) {
			c.Endpoint = netip.MustParseAddrPort("0.1.2.3:53")
			c.DestinationScope = ExactEndpoint
		},
		"missing source":      func(b *Binding, c *Configuration) { b.Source = netip.Addr{} },
		"same source":         func(b *Binding, c *Configuration) { b.Source = c.Endpoint.Addr() },
		"source outside":      func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("198.51.100.10") },
		"source family":       func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("2001:db8::10") },
		"interface":           func(b *Binding, c *Configuration) { b.Observer.InterfaceIndex = 0 },
		"interface overflow":  func(b *Binding, c *Configuration) { b.Observer.InterfaceIndex = 2147483648 },
		"observer":            func(b *Binding, c *Configuration) { b.Observer.SensorID = "" },
		"no prefixes":         func(b *Binding, c *Configuration) { b.Prefixes = nil },
		"duplicate prefixes":  func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, b.Prefixes[0]) },
		"host bits":           func(b *Binding, c *Configuration) { b.Prefixes = []string{"192.0.2.10/24"} },
		"default route":       func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "0.0.0.0/0") },
		"range into reserved": func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "128.0.0.0/1") },
		"IPv6 into multicast": func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "e000::/3") },
		"network target":      func(b *Binding, c *Configuration) { c.Endpoint = netip.MustParseAddrPort("192.0.2.0:53") },
		"broadcast target":    func(b *Binding, c *Configuration) { c.Endpoint = netip.MustParseAddrPort("192.0.2.255:53") },
		"network source":      func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("192.0.2.0") },
		"overlap target veto": func(b *Binding, c *Configuration) {
			b.Prefixes = append(b.Prefixes, "192.0.2.52/31", "192.0.2.52/30")
			c.Endpoint = netip.MustParseAddrPort("192.0.2.52:53")
		},
		"overlap source veto": func(b *Binding, c *Configuration) {
			b.Prefixes = append(b.Prefixes, "192.0.2.8/30")
			b.Source = netip.MustParseAddr("192.0.2.11")
		},
		"many prefixes": func(b *Binding, c *Configuration) { b.Prefixes = make([]string, 33) },
	}
	for label, mut := range mutations {
		t.Run(label, func(t *testing.T) {
			b, c := fixture()
			mut(&b, &c)
			p, e := New(b, c, at)
			if e == nil || p.Current(at) {
				t.Fatal("accepted invalid plan")
			}
			if strings.Contains(e.Error(), "192.") || strings.Contains(e.Error(), "test.example") {
				t.Fatal("sensitive error")
			}
		})
	}
}

func TestFreshnessAndRevalidation(t *testing.T) {
	b, c := fixture()
	p := mustPlan(t, b, c, at)
	for _, tt := range []struct {
		now  time.Time
		want bool
	}{{at, true}, {at.Add(ReviewLifetime - time.Nanosecond), true}, {at.Add(ReviewLifetime), false}, {at.Add(-time.Nanosecond), false}, {time.Time{}, false}} {
		if p.Current(tt.now) != tt.want {
			t.Fatalf("freshness %s", tt.now)
		}
	}
	for _, now := range []time.Time{time.Time{}, time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC), time.Unix(0, 1<<63-1).Add(-time.Second)} {
		if _, e := New(b, c, now); e != ErrClock {
			t.Fatalf("clock accepted %s: %v", now, e)
		}
	}
	newer := mustPlan(t, b, c, at.Add(time.Second))
	if !p.SameSelection(newer) {
		t.Fatal("review time changes selection")
	}
	b.Prefixes = append(b.Prefixes, "198.51.100.0/24")
	p = mustPlan(t, b, c, at)
	b.Prefixes[0], b.Prefixes[1] = b.Prefixes[1], b.Prefixes[0]
	if !p.SameSelection(mustPlan(t, b, c, at)) {
		t.Fatal("prefix order changes selection")
	}
	mutations := []func(*Binding, *Configuration){
		func(b *Binding, c *Configuration) { c.Endpoint = netip.MustParseAddrPort("192.0.2.54:53") },
		func(b *Binding, c *Configuration) { c.Name = "other.example." },
		func(b *Binding, c *Configuration) { c.Selection.QueryID = "query-v2" },
		func(b *Binding, c *Configuration) { c.Selection.ResolverID = "resolver-v2" },
		func(b *Binding, c *Configuration) { c.Selection.QueryType = nq.DNSQueryAAAA },
		func(b *Binding, c *Configuration) { c.Selection.Expect = nq.DNSExpectNXDOMAIN },
		func(b *Binding, c *Configuration) { c.DestinationScope = ExactEndpoint },
		func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("192.0.2.11") },
		func(b *Binding, c *Configuration) { b.Observer.InterfaceIndex++ },
		func(b *Binding, c *Configuration) { b.Observer.ScopeID = "home2" },
		func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "203.0.113.0/24") },
	}
	for i, mut := range mutations {
		b, c := fixture()
		original := mustPlan(t, b, c, at)
		mut(&b, &c)
		if original.SameSelection(mustPlan(t, b, c, at)) {
			t.Fatalf("missed selection change %d", i)
		}
	}
	if (Plan{}).Current(at) || (Plan{}).SameSelection(Plan{}) {
		t.Fatal("zero plan valid")
	}
}

func TestSubnetBoundaries(t *testing.T) {
	b, c := fixture()
	b.Prefixes = append(b.Prefixes, "128.0.0.0/2")
	if _, e := New(b, c, at); e != ErrBinding {
		t.Fatal("broad prefix covers excluded link-local range")
	}
	b, c = fixture()
	b.Prefixes = []string{"192.0.2.52/31"}
	b.Source = netip.MustParseAddr("192.0.2.52")
	mustPlan(t, b, c, at)
	b.Prefixes = []string{"192.0.2.52/32", "192.0.2.53/32"}
	mustPlan(t, b, c, at)
	b, c = fixture()
	b.Source = netip.MustParseAddr("2001:db8:1::10")
	b.Prefixes = []string{"2001:db8:1::/64"}
	c.Endpoint = netip.MustParseAddrPort("[2001:db8:1::]:53")
	c.Selection.Family = nq.FamilyIPv6
	if _, e := New(b, c, at); e != ErrBinding {
		t.Fatal("accepted subnet-router anycast")
	}
	c.Endpoint = netip.MustParseAddrPort("[2001:db8:1::53]:53")
	b.Source = netip.MustParseAddr("2001:db8:1::")
	if _, e := New(b, c, at); e != ErrBinding {
		t.Fatal("accepted anycast source")
	}
	b.Source = netip.MustParseAddr("2001:db8:1::10")
	b.Prefixes = []string{"2001:db8:1::10/128", "2001:db8:1::53/128"}
	mustPlan(t, b, c, at)
	b.Prefixes = []string{"2001:0db8:1::/64"}
	if _, e := New(b, c, at); e != ErrBinding {
		t.Fatal("accepted noncanonical prefix")
	}
}
