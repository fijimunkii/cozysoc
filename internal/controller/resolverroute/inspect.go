// Package resolverroute reads local route/interface metadata. It sends no IP
// packets, changes no routes, and grants no consent or socket-binding assurance.
package resolverroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverwire"
)

var (
	ErrUnsupported = errors.New("resolver route inspection is unsupported")
	ErrUnavailable = errors.New("resolver route metadata is unavailable")
	ErrPermission  = errors.New("resolver route metadata requires permission")
	ErrNoRoute     = errors.New("resolver route lookup found no route")
	ErrMismatch    = errors.New("resolver route or source does not match enrollment")
)

const inspectionTimeout = 2 * time.Second
const maxInterfaceAddresses = 128

// Enrollment must be selected and freshly verified by the controller, not a
// caller-provided substitute for its stored authorized scope.
type Enrollment struct {
	Observer nq.Observer
	Prefixes []string
}
type Inspector interface {
	Inspect(context.Context, Enrollment, resolverplan.Configuration) (resolverrun.Selection, error)
}
type localInterface struct {
	name      string
	index     int
	flags     net.Flags
	addresses []netip.Prefix
}
type observedRoute struct {
	destination            netip.Prefix
	source                 netip.Addr
	name                   string
	index                  int
	gateway                netip.Addr
	gatewayZone, linkIndex int
}
type inspector struct {
	lookup        func(context.Context, netip.Addr) (observedRoute, error)
	readInterface func(context.Context, string) (localInterface, error)
	now           func() time.Time
}

func validTime(t time.Time) bool { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }
func (i inspector) Inspect(ctx context.Context, e Enrollment, c resolverplan.Configuration) (resolverrun.Selection, error) {
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	if i.lookup == nil || i.readInterface == nil || i.now == nil {
		return resolverrun.Selection{}, ErrUnavailable
	}
	start := i.now()
	if !validTime(start) || !validTime(start.Add(resolverplan.ReviewLifetime)) {
		return resolverrun.Selection{}, ErrUnavailable
	}
	snapshot := nq.ResolverSnapshot{Observer: e.Observer, Selections: []nq.ResolverSelection{c.Selection}, AsOf: start, WindowStart: start, Freshness: time.Second}
	if nq.ValidateResolverSnapshot(snapshot) != nil || len(e.Prefixes) == 0 || len(e.Prefixes) > 32 || c.Selection.Transport != nq.DNSUDP || (c.DestinationScope != resolverplan.EnrolledPrefix && c.DestinationScope != resolverplan.ExactEndpoint) {
		return resolverrun.Selection{}, ErrMismatch
	}
	for _, p := range e.Prefixes {
		if len(p) > 64 {
			return resolverrun.Selection{}, ErrMismatch
		}
	}
	if _, err := resolverwire.NewQuery(c.Endpoint, c.Name, c.Selection.QueryType, 0); err != nil {
		return resolverrun.Selection{}, ErrMismatch
	}
	e.Prefixes = slices.Clone(e.Prefixes)
	ctx, cancel := context.WithTimeout(ctx, inspectionTimeout)
	defer cancel()
	before, err := i.readInterface(ctx, e.Observer.InterfaceName)
	if err != nil {
		return resolverrun.Selection{}, err
	}
	if !matchesEnrollment(e, before) {
		return resolverrun.Selection{}, ErrMismatch
	}
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	r, err := i.lookup(ctx, c.Endpoint.Addr())
	if err != nil {
		return resolverrun.Selection{}, err
	}
	observed := i.now()
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	after, err := i.readInterface(ctx, e.Observer.InterfaceName)
	if err != nil {
		return resolverrun.Selection{}, err
	}
	now := i.now()
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	if !validTime(observed) || !validTime(now) || observed.Before(start) || now.Before(observed) || now.Sub(start) >= inspectionTimeout || now.Round(0).Sub(start.Round(0)) >= inspectionTimeout {
		return resolverrun.Selection{}, ErrUnavailable
	}
	if !matchesEnrollment(e, after) || !matchesRoute(e, c.Endpoint.Addr(), r, before, after) {
		return resolverrun.Selection{}, ErrMismatch
	}
	p, err := resolverplan.New(resolverplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: r.source}, c, now)
	if err != nil {
		return resolverrun.Selection{}, ErrMismatch
	}
	return resolverrun.Selection{Plan: p, RouteObservedAt: observed.Round(0).UTC(), RouteFreshUntil: observed.Round(0).UTC().Add(resolverplan.ReviewLifetime)}, nil
}
func matchesEnrollment(e Enrollment, l localInterface) bool {
	if l.name != e.Observer.InterfaceName || l.index != e.Observer.InterfaceIndex || l.flags&net.FlagUp == 0 || l.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 || len(l.addresses) == 0 || len(l.addresses) > maxInterfaceAddresses {
		return false
	}
	set := make(map[string]bool)
	for _, p := range l.addresses {
		if !p.IsValid() || p.Addr().Is4In6() {
			return false
		}
		a := p.Addr()
		if a.IsGlobalUnicast() && !a.IsLoopback() && !a.IsLinkLocalUnicast() {
			set[p.Masked().String()] = true
		}
	}
	if len(set) != len(e.Prefixes) {
		return false
	}
	for _, p := range e.Prefixes {
		if !set[p] {
			return false
		}
		delete(set, p)
	}
	return len(set) == 0
}
func matchesRoute(e Enrollment, target netip.Addr, r observedRoute, before, after localInterface) bool {
	if r.name != e.Observer.InterfaceName || r.index != e.Observer.InterfaceIndex || !r.destination.IsValid() || r.destination != r.destination.Masked() || !r.destination.Contains(target) || !r.source.IsValid() || r.source.Is4() != target.Is4() {
		return false
	}
	if r.gateway.IsValid() {
		if r.linkIndex != 0 || r.gateway.Is4() != target.Is4() || r.gateway.IsUnspecified() || r.gateway.IsLoopback() || r.gateway.IsMulticast() || r.gateway.Is4In6() || r.gateway == r.source || r.gateway == target {
			return false
		}
		if r.gateway.IsLinkLocalUnicast() {
			if r.gateway.Is4() || r.gatewayZone != r.index {
				return false
			}
		} else if !r.gateway.IsGlobalUnicast() || r.gatewayZone != 0 {
			return false
		}
	} else if r.linkIndex != r.index {
		return false
	}
	for _, l := range []localInterface{before, after} {
		source, gateway := false, false
		for _, p := range l.addresses {
			if p.Addr() == target || (r.gateway.IsValid() && p.Addr() == r.gateway) {
				return false
			}
			if p.Addr() == r.source && (r.gateway.IsValid() || p.Contains(target)) {
				source = true
			}
			if r.gateway.IsValid() && p.Contains(r.gateway) {
				gateway = true
			}
		}
		if !source || (r.gateway.IsValid() && !gateway) {
			return false
		}
	}
	return true
}
func readSystemInterface(ctx context.Context, name string) (localInterface, error) {
	if ctx.Err() != nil {
		return localInterface{}, ctx.Err()
	}
	i, err := net.InterfaceByName(name)
	if err != nil {
		return localInterface{}, ErrUnavailable
	}
	addresses, err := i.Addrs()
	if err != nil || len(addresses) > maxInterfaceAddresses {
		return localInterface{}, ErrUnavailable
	}
	l := localInterface{name: i.Name, index: i.Index, flags: i.Flags}
	for _, a := range addresses {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			return localInterface{}, ErrUnavailable
		}
		l.addresses = append(l.addresses, p)
	}
	if ctx.Err() != nil {
		return localInterface{}, ctx.Err()
	}
	return l, nil
}
