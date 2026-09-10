// Package gatewayroute reads local route/interface metadata for a gateway review.
// It does not send IP packets, grant consent, or bind a future probe socket.
package gatewayroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var (
	ErrUnsupported = errors.New("gateway route inspection is unsupported")
	ErrUnavailable = errors.New("gateway route metadata is unavailable")
	ErrPermission  = errors.New("gateway route metadata requires permission")
	ErrNoRoute     = errors.New("gateway route lookup found no route")
	ErrMismatch    = errors.New("gateway route or source does not match enrollment")
)

const inspectionTimeout = 2 * time.Second
const maxInterfaceAddresses = 128

// Evidence describes a routing-table interface address, NOT the source address
// of a bound/send-capable socket. It cannot prove gateway role or reachability.
type Evidence struct {
	InterfaceName  string
	InterfaceIndex int
	SourceAddress  netip.Addr
	ObservedAt     time.Time
	FreshUntil     time.Time
}

type Inspector interface {
	Inspect(context.Context, networkquality.GatewayPlanBinding, netip.Addr) (Evidence, error)
}

type localInterface struct {
	name      string
	index     int
	flags     net.Flags
	addresses []netip.Prefix // Keep host bits to verify the route's IFA, not just CIDRs.
}

type route struct {
	destination    netip.Prefix
	interfaceName  string
	interfaceIndex int
	source         netip.Addr
	flags          uint32
	linkIndex      int // Nonzero only for an AF_LINK next-hop on this interface.
}

type inspector struct {
	lookup        func(context.Context, netip.Addr) (route, error)
	readInterface func(context.Context, string) (localInterface, error)
	now           func() time.Time
}

func (i inspector) Inspect(ctx context.Context, binding networkquality.GatewayPlanBinding, target netip.Addr) (Evidence, error) {
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	if i.lookup == nil || i.readInterface == nil || i.now == nil {
		return Evidence{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, inspectionTimeout)
	defer cancel()
	started := i.now().UTC()
	plan, err := networkquality.PreviewGatewayCheck(binding, target.String(), started)
	if err != nil {
		return Evidence{}, ErrUnavailable
	}
	binding = plan.Binding
	before, err := i.readInterface(ctx, binding.InterfaceName)
	if err != nil {
		return Evidence{}, err
	}
	if !matchesBinding(binding, before) {
		return Evidence{}, ErrMismatch
	}
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	observed, err := i.lookup(ctx, target)
	if err != nil {
		return Evidence{}, err
	}
	observedAt := i.now().UTC() // Never refresh route time during later revalidation.
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	after, err := i.readInterface(ctx, binding.InterfaceName)
	if err != nil {
		return Evidence{}, err
	}
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	ended := i.now().UTC()
	if observedAt.Before(started) || ended.Before(observedAt) || ended.Sub(started) > inspectionTimeout ||
		observedAt.IsZero() || observedAt.Add(networkquality.GatewayReviewLifetime).Year() > 9999 {
		return Evidence{}, ErrUnavailable
	}
	if !matchesBinding(binding, after) || !matchesRoute(binding, target, observed, before, after, observedAt) {
		return Evidence{}, ErrMismatch
	}
	return Evidence{InterfaceName: observed.interfaceName, InterfaceIndex: observed.interfaceIndex,
		SourceAddress: observed.source, ObservedAt: observedAt,
		FreshUntil: observedAt.Add(networkquality.GatewayReviewLifetime)}, nil
}

func matchesBinding(binding networkquality.GatewayPlanBinding, local localInterface) bool {
	if local.name != binding.InterfaceName || local.index != binding.InterfaceIndex ||
		local.flags&net.FlagUp == 0 || local.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 ||
		len(local.addresses) == 0 || len(local.addresses) > maxInterfaceAddresses {
		return false
	}
	current := make(map[string]struct{}, len(local.addresses))
	for _, prefix := range local.addresses {
		if !prefix.IsValid() || prefix.Addr().Is4In6() {
			return false
		}
		address := prefix.Masked().Addr()
		if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
			continue
		}
		current[prefix.Masked().String()] = struct{}{}
	}
	if len(current) != len(binding.Prefixes) {
		return false
	}
	for _, prefix := range binding.Prefixes {
		if _, ok := current[prefix]; !ok {
			return false
		}
	}
	return true
}

func matchesRoute(binding networkquality.GatewayPlanBinding, target netip.Addr, r route, before, after localInterface, observedAt time.Time) bool {
	// Permit only understood, directly-connected route flags. In particular, never
	// force ifscope to override a VPN/off-interface route, or accept gateway hops,
	// redirects, reject/blackhole/local/broadcast/multicast/proxy/unknown semantics.
	const allowed = flagUp | flagHost | flagDone | flagCloning | flagLLInfo | flagStatic |
		flagPrCloning | flagWasCloned | flagIfScope | flagIfRef | flagRouter | flagPinned
	if r.flags&(flagUp|flagDone) != flagUp|flagDone || r.flags&^uint32(allowed) != 0 ||
		r.interfaceName != binding.InterfaceName || r.interfaceIndex != binding.InterfaceIndex ||
		r.linkIndex != binding.InterfaceIndex || !r.destination.IsValid() || !r.destination.Addr().Is4() ||
		!r.destination.Contains(target) || !r.source.Is4() || !r.source.IsPrivate() || r.source == target {
		return false
	}
	// The route-associated address must still exist on the enrolled interface both
	// before and after lookup. Do not guess among addresses or use masked prefixes
	// as proof that an exact host address is assigned.
	for _, local := range []localInterface{before, after} {
		found := false
		for _, p := range local.addresses {
			if p.Addr() == target {
				return false
			}
			if p.Addr() == r.source && p.Contains(target) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	// Apply the same private-host and overlapping-prefix restrictions to the source.
	_, err := networkquality.PreviewGatewayCheck(binding, r.source.String(), observedAt)
	return err == nil
}

func readSystemInterface(ctx context.Context, name string) (localInterface, error) {
	if err := ctx.Err(); err != nil {
		return localInterface{}, err
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return localInterface{}, ErrUnavailable
	}
	addresses, err := iface.Addrs()
	if err != nil || len(addresses) > maxInterfaceAddresses {
		return localInterface{}, ErrUnavailable
	}
	result := localInterface{name: iface.Name, index: iface.Index, flags: iface.Flags,
		addresses: make([]netip.Prefix, 0, len(addresses))}
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil {
			return localInterface{}, ErrUnavailable
		}
		result.addresses = append(result.addresses, prefix)
	}
	if err := ctx.Err(); err != nil {
		return localInterface{}, err
	}
	return result, nil
}
