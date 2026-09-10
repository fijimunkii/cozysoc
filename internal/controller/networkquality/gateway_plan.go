package networkquality

import (
	"errors"
	"net/netip"
	"regexp"
	"sort"
	"time"
)

var (
	ErrGatewayPlanTarget  = errors.New("gateway preview requires one eligible private IPv4 host in the enrolled prefixes")
	ErrGatewayPlanBinding = errors.New("gateway preview binding is invalid")
	ErrGatewayPlanClock   = errors.New("gateway preview time is invalid")
)

const GatewayReviewLifetime = 30 * time.Second

var gatewayScopePattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
var gatewayInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// GatewayPlanBinding describes the enrollment selected by the controller, not
// caller-selected scope or proof of routing. Prefixes must be canonical networks.
type GatewayPlanBinding struct {
	ScopeID        string
	InterfaceName  string
	InterfaceIndex int
	Prefixes       []string
}

// GatewayProbeBudget is a proposed executor ceiling, not evidence of enforcement.
// No executor or traffic-producing function is provided by this package.
type GatewayProbeBudget struct {
	MaxAttempts         int
	MinInterval         time.Duration // between attempt starts
	AttemptTimeout      time.Duration
	TotalTimeout        time.Duration
	PayloadBytes        int
	MaxICMPRequestBytes int // ICMP header + data, excluding IP/link/ARP overhead
	MaxConcurrentRuns   int
	MinRunInterval      time.Duration // controller-wide, not per target
}

// GatewayCheckPlan is a short-lived review artifact, NOT an authorization token.
// The target is user-selected; membership in a prefix does not prove gateway role,
// on-link routing, source address, OS permissions, or the actual send interface.
type GatewayCheckPlan struct {
	Binding         GatewayPlanBinding
	Target          netip.Addr
	CreatedAt       time.Time
	ReviewExpiresAt time.Time
	Budget          GatewayProbeBudget
}

// ValidateGatewayPreviewTarget does no resolution. Hostnames, URLs, ports, CIDRs,
// zones, mapped IPv6, and non-RFC1918 addresses are outside this first profile.
func ValidateGatewayPreviewTarget(raw string) error {
	if len(raw) > 15 {
		return ErrGatewayPlanTarget
	}
	address, err := netip.ParseAddr(raw)
	if err != nil || !address.Is4() || !address.IsPrivate() || address.String() != raw {
		return ErrGatewayPlanTarget
	}
	return nil
}

// PreviewGatewayCheck is pure: it neither inspects interfaces nor sends traffic.
// The caller must first verify the entire current enrolled binding. Reviewing a
// plan is not consent, and no returned field may be used as send authority.
func PreviewGatewayCheck(binding GatewayPlanBinding, target string, now time.Time) (GatewayCheckPlan, error) {
	if err := ValidateGatewayPreviewTarget(target); err != nil {
		return GatewayCheckPlan{}, err
	}
	if !gatewayScopePattern.MatchString(binding.ScopeID) || !gatewayInterfacePattern.MatchString(binding.InterfaceName) ||
		binding.InterfaceIndex < 1 || binding.InterfaceIndex > 2147483647 || len(binding.Prefixes) == 0 || len(binding.Prefixes) > 32 {
		return GatewayCheckPlan{}, ErrGatewayPlanBinding
	}
	now = now.Round(0).UTC()
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 || now.Add(GatewayReviewLifetime).Year() > 9999 {
		return GatewayCheckPlan{}, ErrGatewayPlanClock
	}
	address, _ := netip.ParseAddr(target)
	seen := make(map[netip.Prefix]struct{}, len(binding.Prefixes))
	prefixes := make([]string, 0, len(binding.Prefixes))
	matched := false
	for _, raw := range binding.Prefixes {
		// Bound before parsing; diagnostics never repeat supplied context.
		if len(raw) > 64 {
			return GatewayCheckPlan{}, ErrGatewayPlanBinding
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() ||
			prefix.Addr().IsUnspecified() || prefix.Addr().IsLoopback() || prefix.Addr().IsMulticast() {
			return GatewayCheckPlan{}, ErrGatewayPlanBinding
		}
		if _, duplicate := seen[prefix]; duplicate {
			return GatewayCheckPlan{}, ErrGatewayPlanBinding
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix.String())
		if !prefix.Contains(address) {
			continue
		}
		// Any containing prefix can veto the target: a broader overlap must not
		// turn another subnet's network/broadcast address into an eligible host.
		if prefix.Bits() > 30 || !prefix.Addr().IsPrivate() {
			return GatewayCheckPlan{}, ErrGatewayPlanTarget
		}
		last := gatewayLastIPv4(prefix)
		if !last.IsPrivate() || address == prefix.Addr() || address == last {
			return GatewayCheckPlan{}, ErrGatewayPlanTarget
		}
		matched = true
	}
	if !matched {
		return GatewayCheckPlan{}, ErrGatewayPlanTarget
	}
	sort.Strings(prefixes)
	binding.Prefixes = prefixes // Never retain the caller's slice.
	return GatewayCheckPlan{
		Binding: binding, Target: address, CreatedAt: now, ReviewExpiresAt: now.Add(GatewayReviewLifetime),
		Budget: GatewayProbeBudget{
			MaxAttempts: 3, MinInterval: time.Second, AttemptTimeout: time.Second,
			TotalTimeout: 5 * time.Second, PayloadBytes: 32, MaxICMPRequestBytes: 3 * (8 + 32),
			MaxConcurrentRuns: 1, MinRunInterval: time.Minute,
		},
	}, nil
}

func gatewayLastIPv4(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Addr().As4()
	for bit := prefix.Bits(); bit < 32; bit++ {
		bytes[bit/8] |= 1 << (7 - bit%8)
	}
	return netip.AddrFrom4(bytes)
}
