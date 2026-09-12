// Package resolverplan builds immutable review plans. It performs no I/O and
// grants no consent, enrollment, route evidence or authority to send a query.
package resolverplan

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverwire"
)

const (
	Profile        = "resolver-udp-v1"
	ReviewLifetime = 30 * time.Second
)

var (
	ErrConfiguration = errors.New("resolver review configuration is invalid")
	ErrBinding       = errors.New("resolver review enrollment or source binding is invalid")
	ErrClock         = errors.New("resolver review time is invalid")
)

// DestinationScope is an explicit requested policy, not consent. EnrolledPrefix
// constrains destination membership, not actual routing. ExactEndpoint allows
// only the selected endpoint outside those prefixes, never a range or fallback.
type DestinationScope string

const (
	EnrolledPrefix DestinationScope = "enrolled-prefix"
	ExactEndpoint  DestinationScope = "exact-endpoint"
)

type Binding struct {
	Observer nq.Observer
	Prefixes []string
	Source   netip.Addr
}

type Configuration struct {
	Selection        nq.ResolverSelection
	Endpoint         netip.AddrPort
	Name             string
	DestinationScope DestinationScope
}

// Budget describes ceilings a future executor must enforce. Byte counts cover
// DNS payload only, excluding UDP/IP/link/neighbor-discovery overhead. Upstream
// recursion performed by the selected server is outside the client's control.
type Budget struct {
	MaxSendCalls         int
	MaxRequestBytes      int
	MaxReplyBytes        int
	MaxReceivedDatagrams int
	MaxReceiveCalls      int
	ExchangeTimeout      time.Duration
	TotalTimeout         time.Duration
	MaxConcurrentRuns    int
	MinRunInterval       time.Duration
}

// Disclosure deliberately contains private selection details for explicit local
// review. It must not enter logs, telemetry, generic diagnostics or browser APIs.
// It is a copy and cannot be submitted back as an executable plan.
type Disclosure struct {
	Profile                 string
	Binding                 Binding
	Configuration           Configuration
	OutsideEnrolledPrefixes bool
	MayForwardUpstream      bool
	Budget                  Budget
	CreatedAt               time.Time
	ExpiresAt               time.Time
}

// Plan keeps the configuration immutable. Zero values are invalid. No wire
// decoder exists; display serialization is explicitly separate from authority.
type Plan struct{ review Disclosure }

func (Plan) String() string               { return "[resolver review plan]" }
func (Plan) GoString() string             { return "[resolver review plan]" }
func (Plan) MarshalJSON() ([]byte, error) { return json.Marshal("[resolver review plan]") }

func validTime(t time.Time) bool { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }

// New requires a controller-selected enrollment/source and explicit resolver
// configuration. The caller must obtain fresh route/socket evidence separately.
// References must rotate when endpoint/name configuration changes. No default
// query, resolver, scope policy or expected answer is inferred.
func New(binding Binding, config Configuration, now time.Time) (Plan, error) {
	now = now.Round(0).UTC()
	if !validTime(now) || !validTime(now.Add(ReviewLifetime)) {
		return Plan{}, ErrClock
	}
	if err := ValidateConfiguration(config); err != nil {
		return Plan{}, err
	}
	snapshot := nq.ResolverSnapshot{Observer: binding.Observer, AsOf: now, WindowStart: now, Freshness: time.Second}
	if nq.ValidateResolverSnapshot(snapshot) != nil {
		return Plan{}, ErrBinding
	}
	query, _ := resolverwire.NewQuery(config.Endpoint, config.Name, config.Selection.QueryType, 0)
	target := config.Endpoint.Addr()
	source := binding.Source
	if !eligibleHost(source) || source.Is4() != target.Is4() || source == target || len(binding.Prefixes) == 0 || len(binding.Prefixes) > 32 {
		return Plan{}, ErrBinding
	}
	prefixes := make([]string, 0, len(binding.Prefixes))
	seen := make(map[netip.Prefix]bool)
	sourceMember, targetMember := false, false
	for _, raw := range binding.Prefixes {
		if len(raw) > 64 {
			return Plan{}, ErrBinding
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix != prefix.Masked() || prefix.String() != raw || !eligibleHost(prefix.Addr()) || seen[prefix] {
			return Plan{}, ErrBinding
		}
		// Reject ranges that encompass non-unicast space. Individual destinations
		// and the selected source must additionally be eligible subnet hosts below.
		if !eligibleHost(lastAddress(prefix)) || excludedRange(prefix) {
			return Plan{}, ErrBinding
		}
		seen[prefix] = true
		prefixes = append(prefixes, raw)
		for _, selected := range []struct {
			address netip.Addr
			member  *bool
		}{{source, &sourceMember}, {target, &targetMember}} {
			if !prefix.Contains(selected.address) {
				continue
			}
			if !subnetHost(prefix, selected.address) {
				return Plan{}, ErrBinding
			}
			*selected.member = true
		}
	}
	if !sourceMember || (config.DestinationScope == EnrolledPrefix && !targetMember) {
		return Plan{}, ErrBinding
	}
	sort.Strings(prefixes)
	binding.Prefixes = prefixes
	// The codec lowercases names; obtain its canonical question spelling without
	// retaining its transaction ID or exposing wire bytes in the review.
	config.Name = strings.ToLower(config.Name)
	return Plan{review: Disclosure{Profile: Profile, Binding: binding, Configuration: config,
		OutsideEnrolledPrefixes: !targetMember, MayForwardUpstream: true,
		Budget: Budget{MaxSendCalls: 1, MaxRequestBytes: len(query.Bytes()), MaxReplyBytes: resolverwire.MaxReplyBytes,
			MaxReceivedDatagrams: 16, MaxReceiveCalls: 512, ExchangeTimeout: 2 * time.Second, TotalTimeout: 5 * time.Second,
			MaxConcurrentRuns: 1, MinRunInterval: time.Minute},
		CreatedAt: now, ExpiresAt: now.Add(ReviewLifetime)}}, nil
}

// ValidateConfiguration checks the supported profile without inventing a source,
// route, enrollment or consent. New additionally validates the current binding.
func ValidateConfiguration(config Configuration) error {
	if nq.ValidateResolverSelection(config.Selection) != nil || config.Selection.Transport != nq.DNSUDP ||
		(config.DestinationScope != EnrolledPrefix && config.DestinationScope != ExactEndpoint) {
		return ErrConfiguration
	}
	if _, err := resolverwire.NewQuery(config.Endpoint, config.Name, config.Selection.QueryType, 0); err != nil {
		return ErrConfiguration
	}
	target := config.Endpoint.Addr()
	if !eligibleHost(target) || (target.Is4() && config.Selection.Family != nq.FamilyIPv4) || (target.Is6() && config.Selection.Family != nq.FamilyIPv6) {
		return ErrConfiguration
	}
	return nil
}

func eligibleHost(a netip.Addr) bool {
	if a.Is4() {
		bytes := a.As4()
		if bytes[0] == 0 || bytes[0] >= 224 {
			return false
		}
	}
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsLoopback() && !a.Is4In6() && a.Zone() == "" && !a.IsLinkLocalUnicast()
}

// Address endpoints alone cannot detect an excluded range inside a broad prefix.
func excludedRange(p netip.Prefix) bool {
	for _, raw := range []string{"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/3", "::/128", "::1/128", "::ffff:0:0/96", "fe80::/10", "ff00::/8"} {
		if p.Overlaps(netip.MustParsePrefix(raw)) {
			return true
		}
	}
	return false
}

func lastAddress(p netip.Prefix) netip.Addr {
	if p.Addr().Is4() {
		b := p.Addr().As4()
		for bit := p.Bits(); bit < 32; bit++ {
			b[bit/8] |= 1 << (7 - bit%8)
		}
		return netip.AddrFrom4(b)
	}
	b := p.Addr().As16()
	for bit := p.Bits(); bit < 128; bit++ {
		b[bit/8] |= 1 << (7 - bit%8)
	}
	return netip.AddrFrom16(b)
}

func subnetHost(p netip.Prefix, a netip.Addr) bool {
	if a.Is6() {
		return p.Bits() == 128 || a != p.Addr()
	} // Exclude subnet-router anycast.
	if p.Bits() >= 31 {
		return true
	} // RFC 3021 /31 and explicit /32 host routes.
	return a != p.Addr() && a != lastAddress(p)
}

// Disclosure returns an owned copy. Callers must show both the exact query and
// endpoint, scope policy, upstream-forwarding caveat and complete budget.
func (p Plan) Disclosure() Disclosure {
	d := p.review
	d.Binding.Prefixes = slices.Clone(d.Binding.Prefixes)
	return d
}

// Current is false at the exact expiry boundary and on clock reversal. It is
// only review freshness, never authorization or proof of a usable route.
func (p Plan) Current(now time.Time) bool {
	return p.review.Profile == Profile && validTime(now) && !now.Before(p.review.CreatedAt) && now.Before(p.review.ExpiresAt)
}

// SameSelection compares all pinned execution-relevant values, ignoring only
// review times. A newly constructed preflight plan can detect changed selection,
// enrollment, source, scope policy or budget. This does not renew old consent.
func (p Plan) SameSelection(other Plan) bool {
	if p.review.Profile != Profile || other.review.Profile != Profile {
		return false
	}
	a, b := p.review, other.review
	return a.Profile == b.Profile && a.Configuration == b.Configuration && a.Binding.Observer == b.Binding.Observer &&
		a.Binding.Source == b.Binding.Source && slices.Equal(a.Binding.Prefixes, b.Binding.Prefixes) &&
		a.Budget == b.Budget && a.OutsideEnrolledPrefixes == b.OutsideEnrolledPrefixes && a.MayForwardUpstream == b.MayForwardUpstream
}
