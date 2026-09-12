package resolverroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
)

func fixture() (Enrollment, resolverplan.Configuration, localInterface, observedRoute) {
	return Enrollment{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}}, resolverplan.Configuration{Selection: nq.ResolverSelection{ID: "dns1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Endpoint: netip.MustParseAddrPort("192.0.2.53:53"), Name: "test.example.", DestinationScope: resolverplan.EnrolledPrefix}, localInterface{name: "fixture0", index: 7, flags: net.FlagUp, addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}}, observedRoute{destination: netip.MustParsePrefix("192.0.2.0/24"), source: netip.MustParseAddr("192.0.2.10"), name: "fixture0", index: 7, linkIndex: 7}
}
func TestInspectDirectAndRoutedFamilies(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		for _, routed := range []bool{false, true} {
			e, c, l, r := fixture()
			if v6 {
				e.Prefixes = []string{"2001:db8:1::/64"}
				c.Endpoint = netip.MustParseAddrPort("[2001:db8:1::53]:53")
				c.Selection.Family = nq.FamilyIPv6
				l.addresses = []netip.Prefix{netip.MustParsePrefix("2001:db8:1::10/64"), netip.MustParsePrefix("fe80::10/64")}
				r.destination = netip.MustParsePrefix("2001:db8:1::/64")
				r.source = netip.MustParseAddr("2001:db8:1::10")
			}
			if routed {
				r.linkIndex = 0
				c.DestinationScope = resolverplan.ExactEndpoint
				if v6 {
					r.gateway = netip.MustParseAddr("fe80::1")
					r.gatewayZone = 7
					r.destination = netip.MustParsePrefix("::/0")
					c.Endpoint = netip.MustParseAddrPort("[2001:db8:2::53]:53")
				} else {
					r.gateway = netip.MustParseAddr("192.0.2.1")
					r.destination = netip.MustParsePrefix("0.0.0.0/0")
					c.Endpoint = netip.MustParseAddrPort("198.51.100.53:53")
				}
			}
			now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			reads := 0
			i := inspector{now: func() time.Time { return now }, lookup: func(context.Context, netip.Addr) (observedRoute, error) {
				now = now.Add(time.Millisecond)
				return r, nil
			}, readInterface: func(context.Context, string) (localInterface, error) { reads++; return l, nil }}
			result, err := i.Inspect(context.Background(), e, c)
			if err != nil || result.Plan.Disclosure().Binding.Source != r.source || reads != 2 || !result.RouteFreshUntil.Equal(result.RouteObservedAt.Add(30*time.Second)) {
				t.Fatalf("v6=%t routed=%t: %v", v6, routed, err)
			}
		}
	}
}
func TestRejectRouteEnrollmentAndSourceChanges(t *testing.T) {
	mutations := map[string]func(*Enrollment, *resolverplan.Configuration, *localInterface, *observedRoute){
		"VPN interface": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) { r.index++ },
		"VPN flags": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			l.flags |= net.FlagPointToPoint
		},
		"down": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) { l.flags = 0 },
		"different source": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			r.source = netip.MustParseAddr("192.0.2.11")
		},
		"self target": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			c.Endpoint = netip.MustParseAddrPort("192.0.2.10:53")
		},
		"wrong prefix": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			e.Prefixes = []string{"198.51.100.0/24"}
		},
		"route misses target": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			r.destination = netip.MustParsePrefix("198.51.100.0/24")
		},
		"wrong link index": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) { r.linkIndex++ },
		"offlink gateway": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			r.linkIndex = 0
			r.gateway = netip.MustParseAddr("198.51.100.1")
		},
		"self gateway": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			r.linkIndex = 0
			r.gateway = r.source
		},
		"duplicate enrollment": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			e.Prefixes = append(e.Prefixes, e.Prefixes[0])
		},
		"unexpected interface address": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			l.addresses = append(l.addresses, netip.MustParsePrefix("198.51.100.10/24"))
		},
		"missing scope policy": func(e *Enrollment, c *resolverplan.Configuration, l *localInterface, r *observedRoute) {
			c.DestinationScope = ""
		},
	}
	for label, mut := range mutations {
		t.Run(label, func(t *testing.T) {
			e, c, l, r := fixture()
			mut(&e, &c, &l, &r)
			i := inspector{now: time.Now, lookup: func(context.Context, netip.Addr) (observedRoute, error) { return r, nil }, readInterface: func(context.Context, string) (localInterface, error) { return l, nil }}
			if _, err := i.Inspect(context.Background(), e, c); !errors.Is(err, ErrMismatch) {
				t.Fatalf("accepted invalid route: %v", err)
			}
		})
	}
}
func TestInterfaceRaceCancellationAndClockBounds(t *testing.T) {
	for _, mode := range []string{"address-removed", "prefix-added", "canceled", "clock-backward", "clock-forward"} {
		t.Run(mode, func(t *testing.T) {
			e, c, l, r := fixture()
			now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			i := inspector{now: func() time.Time { return now }, lookup: func(context.Context, netip.Addr) (observedRoute, error) {
				switch mode {
				case "canceled":
					cancel()
				case "clock-backward":
					now = now.Add(-time.Second)
				case "clock-forward":
					now = now.Add(2 * time.Second)
				}
				return r, nil
			}, readInterface: func(context.Context, string) (localInterface, error) {
				reads++
				copy := l
				if reads == 2 {
					switch mode {
					case "address-removed":
						copy.addresses = []netip.Prefix{netip.MustParsePrefix("192.0.2.11/24")}
					case "prefix-added":
						copy.addresses = append(copy.addresses, netip.MustParsePrefix("198.51.100.10/24"))
					}
				}
				return copy, nil
			}}
			if _, err := i.Inspect(ctx, e, c); err == nil {
				t.Fatal("accepted changed metadata")
			}
		})
	}
}
