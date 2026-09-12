// Package checkroute reads local route/interface metadata. It sends no IP
// packets, changes no routes, and grants no consent or socket-binding assurance.
package checkroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/checkbinding"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var (
	ErrUnsupported = errors.New("check route inspection is unsupported")
	ErrUnavailable = errors.New("check route metadata is unavailable")
	ErrPermission  = errors.New("check route metadata requires permission")
	ErrNoRoute     = errors.New("check route lookup found no route")
	ErrMismatch    = errors.New("check route or source does not match enrollment")
)

const Freshness = 30 * time.Second

const inspectionTimeout = 2 * time.Second
const maxInterfaceAddresses = 128

// Enrollment must be selected and freshly verified by the controller, not a
// caller-provided substitute for its stored authorized scope.
type Enrollment struct {
	Observer nq.Observer
	Prefixes []string
}
type Inspector interface {
	Inspect(context.Context, Enrollment, netip.Addr) (Evidence, error)
}

// Evidence is a bounded local metadata sample, not socket or execution authority.
// Consumers must revalidate their own configuration and retain these timestamps.
type Evidence struct {
	Source                              netip.Addr
	ObservedAt, FreshUntil, CompletedAt time.Time
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
func (i inspector) Inspect(ctx context.Context, e Enrollment, target netip.Addr) (Evidence, error) {
	if ctx.Err() != nil {
		return Evidence{}, ctx.Err()
	}
	if i.lookup == nil || i.readInterface == nil || i.now == nil {
		return Evidence{}, ErrUnavailable
	}
	start := i.now()
	if !validTime(start) || !validTime(start.Add(Freshness)) {
		return Evidence{}, ErrUnavailable
	}
	snapshot := nq.Snapshot{Observer: e.Observer, AsOf: start, WindowStart: start, Freshness: time.Second}
	if nq.ValidateSnapshot(snapshot) != nil || !checkbinding.EligibleHost(target) || len(e.Prefixes) == 0 || len(e.Prefixes) > 32 {
		return Evidence{}, ErrMismatch
	}
	for _, p := range e.Prefixes {
		if len(p) > 64 {
			return Evidence{}, ErrMismatch
		}
	}
	e.Prefixes = slices.Clone(e.Prefixes)
	ctx, cancel := context.WithTimeout(ctx, inspectionTimeout)
	defer cancel()
	before, err := i.readInterface(ctx, e.Observer.InterfaceName)
	if err != nil {
		return Evidence{}, err
	}
	if !matchesEnrollment(e, before) {
		return Evidence{}, ErrMismatch
	}
	if ctx.Err() != nil {
		return Evidence{}, ctx.Err()
	}
	r, err := i.lookup(ctx, target)
	if err != nil {
		return Evidence{}, err
	}
	observed := i.now()
	if ctx.Err() != nil {
		return Evidence{}, ctx.Err()
	}
	after, err := i.readInterface(ctx, e.Observer.InterfaceName)
	if err != nil {
		return Evidence{}, err
	}
	now := i.now()
	if ctx.Err() != nil {
		return Evidence{}, ctx.Err()
	}
	if !validTime(observed) || !validTime(observed.Add(Freshness)) || !validTime(now) || observed.Before(start) || now.Before(observed) || now.Sub(start) >= inspectionTimeout || now.Round(0).Sub(start.Round(0)) >= inspectionTimeout {
		return Evidence{}, ErrUnavailable
	}
	if !matchesEnrollment(e, after) || !matchesRoute(e, target, r, before, after) {
		return Evidence{}, ErrMismatch
	}
	if _, _, err := checkbinding.Validate(r.source, target, e.Prefixes); err != nil {
		return Evidence{}, ErrMismatch
	}
	return Evidence{Source: r.source, ObservedAt: observed.Round(0).UTC(), FreshUntil: observed.Round(0).UTC().Add(Freshness), CompletedAt: now.Round(0).UTC()}, nil
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
