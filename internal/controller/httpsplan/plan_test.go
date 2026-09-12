package httpsplan

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var at = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func fixture() (Binding, Configuration) {
	return Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "en0", InterfaceIndex: 4}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")},
		Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint1", RequestID: "request1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: "Status.Example", RequestTarget: "/check?mode=1", DestinationPolicy: ExactEndpoint}
}
func mustPlan(t testing.TB, b Binding, c Configuration, now time.Time) Plan {
	t.Helper()
	p, err := New(b, c, now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestExactDisclosedRequestAndBudget(t *testing.T) {
	b, c := fixture()
	p := mustPlan(t, b, c, at)
	d := p.Disclosure()
	raw, err := p.RequestBytes()
	if err != nil {
		t.Fatal(err)
	}
	expected := "HEAD /check?mode=1 HTTP/1.1\r\nHost: status.example\r\nUser-Agent: CozySOC-Network-Check/1\r\nConnection: close\r\nAccept: */*\r\nAccept-Encoding: identity\r\nCache-Control: no-store\r\n\r\n"
	if string(raw) != expected {
		t.Fatalf("unexpected synthetic request: %q", raw)
	}
	if d.Configuration.ServerName != "status.example" || d.Configuration.Endpoint != c.Endpoint || d.Configuration.RequestTarget != c.RequestTarget || !d.OutsideEnrolledPrefixes || d.Profile != Profile || !d.ExpiresAt.Equal(at.Add(ReviewLifetime)) {
		t.Fatalf("disclosure: %+v", d)
	}
	wantPolicy := Policy{HTTPVersion: "HTTP/1.1", ALPN: "http/1.1", TrustStore: "system", MinTLSVersion: tls.VersionTLS12, MaxTLSVersion: tls.VersionTLS13, VerifyServerIdentity: true, FreshConnection: true}
	if d.Policy != wantPolicy {
		t.Fatalf("unsafe policy: %+v", d.Policy)
	}
	wantBudget := Budget{MaxConnections: 1, MaxRequests: 1, MaxRequestBytes: len(expected), MaxResponseHeaderBytes: 16384, MaxTransportReadBytes: 131072, MaxTransportWriteBytes: 32768, MaxTransportReadCalls: 512, MaxTransportWriteCalls: 64, ConnectTimeout: 2 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 2 * time.Second, TotalTimeout: 8 * time.Second, MaxConcurrentRuns: 1, MinRunInterval: time.Minute}
	if d.Budget != wantBudget {
		t.Fatalf("budget: %+v", d.Budget)
	}
	for _, phrase := range []string{"network address", "TLS name", "user agent", "TCP retransmissions", "body bytes", "metered"} {
		if !strings.Contains(strings.Join(d.Privacy, " "), phrase) {
			t.Fatalf("missing privacy disclosure %s", phrase)
		}
	}
}
func TestRequestEncodingPreservesOnlySelectedOriginForm(t *testing.T) {
	for _, target := range []string{"/", "/a%20b/%2f?key=%23value&x=one+two", "/check?", "/" + strings.Repeat("a", MaxRequestTargetBytes-1), "/caf%C3%A9", "/path:with@symbols?url=https://example.test/x"} {
		for _, method := range []string{"HEAD", "GET"} {
			b, c := fixture()
			c.RequestTarget = target
			c.Selection.Method = method
			p := mustPlan(t, b, c, at)
			raw, err := p.RequestBytes()
			if err != nil {
				t.Fatal(err)
			}
			r, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if r.RequestURI != target || r.Host != "status.example" || r.Method != method || r.URL.IsAbs() || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || !r.Close {
				t.Fatalf("request changed: %+v", r)
			}
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Accept-Encoding") != "identity" {
				t.Fatal("unexpected ambient request data")
			}
			if p.Disclosure().Budget.MaxRequestBytes != len(raw) || len(raw) > p.Disclosure().Budget.MaxTransportWriteBytes {
				t.Fatal("incorrect request ceiling")
			}
			raw[0] = 'X'
			again, _ := p.RequestBytes()
			if again[0] == 'X' {
				t.Fatal("aliased request bytes")
			}
		}
	}
}
func TestPrivateConfigurationAndPlanRequireExplicitDisclosure(t *testing.T) {
	b, c := fixture()
	b.Prefixes = append(b.Prefixes, "203.0.113.0/24")
	p := mustPlan(t, b, c, at)
	d := p.Disclosure()
	b.Prefixes[0] = "10.0.0.0/8"
	d.Binding.Prefixes[0] = "10.0.0.0/8"
	d.Privacy[0] = "changed"
	d.Configuration.ServerName = "changed.test"
	d.Policy.VerifyServerIdentity = false
	d.Budget.MaxRequests = 100
	fresh := p.Disclosure()
	if fresh.Binding.Prefixes[0] != "192.0.2.0/24" || fresh.Privacy[0] == "changed" || fresh.Configuration.ServerName != "status.example" || !fresh.Policy.VerifyServerIdentity || fresh.Budget.MaxRequests != 1 {
		t.Fatal("caller changed review")
	}
	for _, v := range []any{p, c, &p, &c} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{string(raw), fmt.Sprint(v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v)} {
			for _, secret := range []string{"198.51.100.20", "Status.Example", "status.example", "/check"} {
				if strings.Contains(text, secret) {
					t.Fatal("private configuration leaked through ordinary formatting")
				}
			}
		}
	}
	raw, err := json.Marshal(p.Disclosure())
	if err != nil || !bytes.Contains(raw, []byte("status.example")) || !bytes.Contains(raw, []byte("198.51.100.20:443")) {
		t.Fatal("explicit disclosure lost destination")
	}
	var decoded Plan
	if json.Unmarshal(raw, &decoded) != ErrPlan || decoded.Current(at) || decoded.SameSelection(p) {
		t.Fatal("decoded disclosure became a plan")
	}
	if raw, err := (Plan{}).RequestBytes(); err != ErrPlan || raw != nil {
		t.Fatal("zero plan encoded request")
	}
}
func TestRejectsAmbiguousOrPrivateRequestInputs(t *testing.T) {
	for _, target := range []string{"", "https://elsewhere.test/", "//elsewhere.test/", "*", "relative", "/x#fragment", "/x\r\nHeader: bad", "/x\x00", "/x\t", "/x y", "/raw<value", "/raw>value", "/raw{value", "/raw|value", "/raw^value", "/raw`value", "/raw\"value", "/raw[value", "/?raw={value", "/x\\y", "/x%0d%0aHeader:bad", "/x?y=%00", "/%7f", "/%5C", "/x?bad=%xx", "/%", "/café", "/" + strings.Repeat("a", MaxRequestTargetBytes)} {
		b, c := fixture()
		c.RequestTarget = target
		if _, err := New(b, c, at); err != ErrConfiguration {
			t.Fatalf("accepted invalid request target %q: %v", target, err)
		}
	}
	for _, name := range []string{"", "localhost", "127.0.0.1", "::1", "status.example.", "*.example", "user@status.example", "https://status.example", "status.example:443", "a..example", "-a.example", "a-.example", "a_b.example", "bad\r\nHost:other", "café.example", strings.Repeat("a", 64) + ".test", strings.Repeat("a.", 127) + "test"} {
		b, c := fixture()
		c.ServerName = name
		if _, err := New(b, c, at); err != ErrConfiguration {
			t.Fatalf("accepted invalid TLS name %q: %v", name, err)
		}
	}
}
func TestExactAddressAndEnrollmentBinding(t *testing.T) {
	b, c := fixture()
	c.Endpoint = netip.MustParseAddrPort("192.0.2.20:443")
	if mustPlan(t, b, c, at).Disclosure().OutsideEnrolledPrefixes {
		t.Fatal("enrolled target mislabeled")
	}
	b.Source = netip.MustParseAddr("2001:db8:1::10")
	b.Prefixes = []string{"2001:db8:1::/64"}
	c.Endpoint = netip.MustParseAddrPort("[2001:db8:2::20]:443")
	c.Selection.Family = nq.FamilyIPv6
	if !mustPlan(t, b, c, at).Disclosure().OutsideEnrolledPrefixes {
		t.Fatal("outside IPv6 target hidden")
	}
	c.Endpoint = netip.MustParseAddrPort("[2001:db8:1::20]:443")
	if mustPlan(t, b, c, at).Disclosure().OutsideEnrolledPrefixes {
		t.Fatal("inside IPv6 target hidden")
	}
	for _, address := range []string{"127.0.0.1:443", "169.254.1.1:443", "0.1.2.3:443", "224.0.0.1:443", "240.0.0.1:443", "[::]:443", "[::1]:443", "[fe80::1]:443", "[fe80::1%en0]:443", "[ff02::1]:443", "[::ffff:192.0.2.20]:443", "198.51.100.20:80", "198.51.100.20:0"} {
		b, c := fixture()
		c.Endpoint = netip.MustParseAddrPort(address)
		if _, err := New(b, c, at); err != ErrConfiguration {
			t.Fatalf("accepted invalid endpoint %s: %v", address, err)
		}
	}
}
func TestRejectsChangedOrMissingBindingAndPolicy(t *testing.T) {
	for name, edit := range map[string]func(*Binding, *Configuration){
		"no policy":                func(b *Binding, c *Configuration) { c.DestinationPolicy = "" },
		"range policy":             func(b *Binding, c *Configuration) { c.DestinationPolicy = "anywhere" },
		"missing reference":        func(b *Binding, c *Configuration) { c.Selection.EndpointID = "" },
		"wrong family":             func(b *Binding, c *Configuration) { c.Selection.Family = nq.FamilyIPv6 },
		"method":                   func(b *Binding, c *Configuration) { c.Selection.Method = "POST" },
		"no expected status":       func(b *Binding, c *Configuration) { c.Selection.ExpectedStatus = 0 },
		"missing endpoint":         func(b *Binding, c *Configuration) { c.Endpoint = netip.AddrPort{} },
		"missing source":           func(b *Binding, c *Configuration) { b.Source = netip.Addr{} },
		"unassigned source":        func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("203.0.113.10") },
		"source is destination":    func(b *Binding, c *Configuration) { b.Source = c.Endpoint.Addr() },
		"source family":            func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("2001:db8::10") },
		"missing scope":            func(b *Binding, c *Configuration) { b.Observer.ScopeID = "" },
		"invalid interface":        func(b *Binding, c *Configuration) { b.Observer.InterfaceName = "bad/name" },
		"invalid index":            func(b *Binding, c *Configuration) { b.Observer.InterfaceIndex = 2147483648 },
		"missing prefixes":         func(b *Binding, c *Configuration) { b.Prefixes = nil },
		"too many prefixes":        func(b *Binding, c *Configuration) { b.Prefixes = make([]string, 33) },
		"duplicate prefixes":       func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, b.Prefixes[0]) },
		"host bits":                func(b *Binding, c *Configuration) { b.Prefixes = []string{"192.0.2.10/24"} },
		"default route":            func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "0.0.0.0/0") },
		"range includes loopback":  func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "64.0.0.0/2") },
		"range includes linklocal": func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "128.0.0.0/2") },
		"source is broadcast":      func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("192.0.2.255") },
		"target is broadcast":      func(b *Binding, c *Configuration) { c.Endpoint = netip.MustParseAddrPort("192.0.2.255:443") },
		"overlap source veto": func(b *Binding, c *Configuration) {
			b.Prefixes = append(b.Prefixes, "192.0.2.10/31", "192.0.2.8/30")
			b.Source = netip.MustParseAddr("192.0.2.11")
		},
	} {
		t.Run(name, func(t *testing.T) {
			b, c := fixture()
			edit(&b, &c)
			p, err := New(b, c, at)
			if err == nil || p.Current(at) {
				t.Fatal("accepted invalid review")
			}
			if strings.Contains(err.Error(), "192.") || strings.Contains(err.Error(), "example") {
				t.Fatal("private error")
			}
		})
	}
}
func TestReviewFreshnessAndExactRevalidation(t *testing.T) {
	b, c := fixture()
	p := mustPlan(t, b, c, at)
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{{at, true}, {at.Add(ReviewLifetime - time.Nanosecond), true}, {at.Add(ReviewLifetime), false}, {at.Add(-time.Nanosecond), false}, {time.Time{}, false}} {
		if p.Current(tc.now) != tc.want {
			t.Fatal("invalid review freshness")
		}
	}
	for _, now := range []time.Time{time.Time{}, time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC), time.Unix(0, 1<<63-1).Add(-time.Second)} {
		if _, err := New(b, c, now); err != ErrClock {
			t.Fatal("invalid clock accepted")
		}
	}
	if !p.SameSelection(mustPlan(t, b, c, at.Add(time.Second))) {
		t.Fatal("time renewed selection identity")
	}
	c.ServerName = strings.ToLower(c.ServerName)
	if !p.SameSelection(mustPlan(t, b, c, at)) {
		t.Fatal("ASCII case changed identity")
	}
	for _, edit := range []func(*Binding, *Configuration){
		func(b *Binding, c *Configuration) { c.Endpoint = netip.MustParseAddrPort("198.51.100.21:443") }, func(b *Binding, c *Configuration) { c.ServerName = "other.example" }, func(b *Binding, c *Configuration) { c.RequestTarget = "/other" },
		func(b *Binding, c *Configuration) { c.Selection.Method = "GET" }, func(b *Binding, c *Configuration) { c.Selection.ExpectedStatus = 404 }, func(b *Binding, c *Configuration) { c.Selection.EndpointID = "endpoint2" }, func(b *Binding, c *Configuration) { c.Selection.RequestID = "request2" }, func(b *Binding, c *Configuration) { c.Selection.ID = "selection2" },
		func(b *Binding, c *Configuration) { b.Source = netip.MustParseAddr("192.0.2.11") }, func(b *Binding, c *Configuration) { b.Observer.InterfaceIndex++ }, func(b *Binding, c *Configuration) { b.Observer.ScopeID = "other" }, func(b *Binding, c *Configuration) { b.Prefixes = append(b.Prefixes, "203.0.113.0/24") },
	} {
		b, c := fixture()
		edit(&b, &c)
		if p.SameSelection(mustPlan(t, b, c, at)) {
			t.Fatal("missed changed selection")
		}
	}
	b, c = fixture()
	b.Prefixes = append(b.Prefixes, "203.0.113.0/24")
	p = mustPlan(t, b, c, at)
	b.Prefixes[0], b.Prefixes[1] = b.Prefixes[1], b.Prefixes[0]
	if !p.SameSelection(mustPlan(t, b, c, at)) {
		t.Fatal("prefix order changed identity")
	}
	if (Plan{}).Current(at) || (Plan{}).SameSelection(Plan{}) {
		t.Fatal("zero plan valid")
	}
}
