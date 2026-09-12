// Package checkbinding validates selected source/target membership in an enrolled
// prefix set. It performs no interface/route inspection and grants no authority.
package checkbinding

import (
	"errors"
	"net/netip"
	"sort"
)

var ErrBinding = errors.New("selected check source or enrolled prefixes are invalid")

// Validate returns an owned, canonical sorted prefix list and whether the exact
// target belongs to it. Both addresses must be eligible unicast hosts of the same
// family. An overlapping subnet can veto a network/broadcast/anycast address even
// if a broader prefix would accept it. Target membership is not routing evidence.
func Validate(sourceAddress, target netip.Addr, enrolled []string) ([]string, bool, error) {
	if !EligibleHost(target) {
		return nil, false, ErrBinding
	}
	source := sourceAddress
	if !EligibleHost(source) || source.Is4() != target.Is4() || source == target || len(enrolled) == 0 || len(enrolled) > 32 {
		return nil, false, ErrBinding
	}
	prefixes := make([]string, 0, len(enrolled))
	seen := make(map[netip.Prefix]bool)
	sourceMember, targetMember := false, false
	for _, raw := range enrolled {
		if len(raw) > 64 {
			return nil, false, ErrBinding
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix != prefix.Masked() || prefix.String() != raw || !EligibleHost(prefix.Addr()) || seen[prefix] {
			return nil, false, ErrBinding
		}
		// Reject ranges that encompass non-unicast space. Individual destinations
		// and the selected source must additionally be eligible subnet hosts below.
		if !EligibleHost(lastAddress(prefix)) || excludedRange(prefix) {
			return nil, false, ErrBinding
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
				return nil, false, ErrBinding
			}
			*selected.member = true
		}
	}
	if !sourceMember {
		return nil, false, ErrBinding
	}
	sort.Strings(prefixes)
	return prefixes, targetMember, nil
}

// EligibleHost rejects non-unicast, mapped, scoped, loopback and link-local addresses.
// It does not establish public-internet routing; private and documentation space
// are permitted for explicit selection and isolated labs.
func EligibleHost(a netip.Addr) bool {
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
