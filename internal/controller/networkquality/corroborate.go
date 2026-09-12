package networkquality

import (
	"fmt"
	"sort"
	"time"
)

// CorroborationInput joins already collected evidence from specialized
// collectors. Device references are supplied by the trusted evidence owner;
// matching scope/interface names alone cannot identify the observing device.
// Nothing in this contract verifies provenance, authorizes traffic or performs I/O.
type CorroborationInput struct {
	HTTPSDeviceID     string
	HTTPS             *HTTPSSnapshot // Optional until an external collector is configured.
	NetworkDeviceID   string
	ResolverDeviceID  string
	Network           Snapshot
	Resolvers         ResolverSnapshot
	Family            AddressFamily
	MaxCompletionSkew time.Duration
}

const MaxCorroborationSkew = 30 * time.Second

type CorroborationEvidence struct {
	HTTPSSelection         *HTTPSSelection
	HTTPSResult            HTTPSResult
	HTTPSStage             HTTPSStage
	HTTPSRequest           HTTPSRequestState
	HTTPStatus             int
	Selection              *ResolverSelection // Owned immutable DNS configuration references.
	Layer                  Layer
	Reference              string
	MeasurementID          string
	Observer               Observer
	Family                 AddressFamily
	Method                 Method
	StartedAt, CompletedAt time.Time
	State                  State
	Gap                    GapReason
	Attempts, Successes    int
	DNSResult              ResolverResult
	ExpectationMatched     *bool
}

type Corroboration struct {
	DiscontinuityID   string
	DiscontinuityAt   time.Time
	DiscontinuityGap  GapReason
	DeviceID          string
	ScopeID           string
	InterfaceName     string
	InterfaceIndex    int
	Family            AddressFamily
	AsOf              time.Time
	MaxCompletionSkew time.Duration
	// Evidence contains the latest selected samples, including stale and explicit
	// gaps. Only fresh, measured entries can support a comparison.
	Evidence                   []CorroborationEvidence
	Compared                   []string // Measurement IDs actually used, never missing/stale entries.
	EvidenceStart, EvidenceEnd time.Time
	Conclusion                 string
	Confidence                 Confidence
	Summary, NextStep          string
	Limitations                []string
}

// Corroborate selects latest evidence before checking freshness and alignment;
// it never searches backward for a convenient success. IPv4/IPv6 are assessed
// separately. Conclusions describe associations, not root causes or outages.
func Corroborate(in CorroborationInput) (Corroboration, error) {
	if err := validateCorroboration(in); err != nil {
		return Corroboration{}, err
	}
	n, err := Assess(in.Network)
	if err != nil {
		return Corroboration{}, err
	}
	d, err := AssessResolvers(in.Resolvers)
	if err != nil {
		return Corroboration{}, err
	}
	out := Corroboration{DeviceID: in.NetworkDeviceID, ScopeID: n.Observer.ScopeID, InterfaceName: n.Observer.InterfaceName, InterfaceIndex: n.Observer.InterfaceIndex,
		Family: in.Family, AsOf: in.Network.AsOf, MaxCompletionSkew: in.MaxCompletionSkew, Evidence: []CorroborationEvidence{}, Compared: []string{},
		Conclusion: "insufficient-evidence", Confidence: ConfidenceUnknown,
		Summary:  "There is not enough comparable evidence from different check layers.",
		NextStep: "Review the missing, stale or unmeasured checks. Any new measurement needs separate authorization.",
		Limitations: []string{
			"Comparisons describe selected checks on one observing device, enrolled scope, interface and address family at the supplied assessment time.",
			"Nearby completion times do not prove simultaneous observations, the same route, independent failures or a common root cause.",
			"The ICMP target's gateway role is unverified. DNS replies, including errors, are responses rather than packet loss.",
			"No conclusion establishes internet availability, captive-portal status, DNSSEC validation, whole-home quality, security or monitoring coverage.",
		}}
	for _, c := range n.Checks {
		if c.Target.Family != in.Family && c.Target.Layer != LayerLink {
			continue
		}
		out.Evidence = append(out.Evidence, CorroborationEvidence{Layer: c.Target.Layer, Reference: c.Target.ID, MeasurementID: c.EvidenceID,
			Observer: n.Observer, Family: c.Target.Family, Method: c.Target.Method, StartedAt: c.StartedAt, CompletedAt: c.CompletedAt, State: c.State, Gap: c.Gap, Attempts: c.Attempts, Successes: c.Successes})
	}
	for _, c := range d.Checks {
		if c.Selection.Family != in.Family {
			continue
		}
		e := CorroborationEvidence{Layer: LayerDNS, Reference: c.Selection.ID, Observer: d.Observer, Family: c.Selection.Family, Method: MethodDNS, State: c.State, DNSResult: c.Result}
		selection := c.Selection
		e.Selection = &selection
		if m := c.Evidence; m != nil {
			e.MeasurementID, e.StartedAt, e.CompletedAt, e.Gap = m.ID, m.StartedAt, m.CompletedAt, m.Gap
			// Preserve expectation independently of freshness: stale evidence remains
			// historical and cannot become a current comparison just because it matched.
			if c.Result == ResolverAnswer || c.Result == ResolverNXDOMAIN || c.Result == ResolverNoData {
				matched := string(c.Result) == string(c.Selection.Expect)
				e.ExpectationMatched = &matched
			}
		}
		out.Evidence = append(out.Evidence, e)
	}
	if in.HTTPS != nil {
		h, err := AssessHTTPS(*in.HTTPS)
		if err != nil {
			return Corroboration{}, err
		}
		for _, c := range h.Checks {
			if c.Selection.Family != in.Family {
				continue
			}
			selection := c.Selection
			e := CorroborationEvidence{Layer: LayerExternal, Reference: selection.ID, HTTPSSelection: &selection,
				Observer: h.Observer, Family: selection.Family, Method: MethodHTTPS, State: c.State, HTTPSResult: c.Result, ExpectationMatched: c.ExpectationMatched}
			if m := c.Evidence; m != nil {
				e.MeasurementID, e.StartedAt, e.CompletedAt, e.Gap = m.ID, m.StartedAt, m.CompletedAt, m.Gap
				e.HTTPSStage, e.HTTPSRequest, e.HTTPStatus = m.Stage, m.Request, m.StatusCode
			}
			out.Evidence = append(out.Evidence, e)
		}
	}
	sort.Slice(out.Evidence, func(i, j int) bool {
		a, b := out.Evidence[i], out.Evidence[j]
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		return a.Reference < b.Reference
	})
	var candidates []CorroborationEvidence
	var firstEnd, lastEnd time.Time
	barrierID, barrier, barrierGap := latestDiscontinuity(in)
	layers := map[Layer]bool{}
	for _, e := range out.Evidence {
		if e.State != StateSucceeded && e.State != StateIssueObserved && !(e.Layer == LayerDNS && e.State == StateUnknown && e.MeasurementID != "" &&
			(e.DNSResult == ResolverTruncated || e.DNSResult == ResolverReferral || e.DNSResult == ResolverUnclassified)) {
			continue
		}
		candidates = append(candidates, e)
		layers[e.Layer] = true
		if firstEnd.IsZero() || e.CompletedAt.Before(firstEnd) {
			firstEnd = e.CompletedAt
		}
		if e.CompletedAt.After(lastEnd) {
			lastEnd = e.CompletedAt
		}
	}
	if len(layers) < 2 {
		return out, nil
	}
	for _, e := range candidates {
		if !barrier.IsZero() && !e.StartedAt.After(barrier) {
			out.Conclusion = "collection-discontinuity"
			out.DiscontinuityID, out.DiscontinuityAt, out.DiscontinuityGap = barrierID, barrier, barrierGap
			out.Summary = "A recorded sleep, offline or network-change gap separates the selected evidence."
			out.NextStep = "Compare evidence collected after the recorded gap. Do not interpret the gap as a measured network outage."
			return out, nil
		}
	}
	if lastEnd.Sub(firstEnd) > in.MaxCompletionSkew {
		out.Conclusion = "observations-too-far-apart"
		out.Summary = "The latest selected measurements ended too far apart for this comparison."
		out.NextStep = "Use evidence from a comparable time window; do not substitute older successes or repeat traffic automatically."
		return out, nil
	}
	linkDown, reply, icmpMiss, dnsIssue, externalMiss := false, false, false, false, false
	dnsReply, icmpReply, externalReply := false, false, false
	failureLayers := map[Layer]bool{}
	allPassed := true
	for _, e := range candidates {
		out.Compared = append(out.Compared, e.MeasurementID)
		if out.EvidenceStart.IsZero() || e.StartedAt.Before(out.EvidenceStart) {
			out.EvidenceStart = e.StartedAt
		}
		if e.CompletedAt.After(out.EvidenceEnd) {
			out.EvidenceEnd = e.CompletedAt
		}
		passed := e.State == StateSucceeded
		switch e.Layer {
		case LayerLink:
			linkDown = linkDown || !passed
		case LayerDNS:
			responded := e.DNSResult == ResolverAnswer || e.DNSResult == ResolverNXDOMAIN || e.DNSResult == ResolverNoData ||
				e.DNSResult == ResolverRefused || e.DNSResult == ResolverServerFailure || e.DNSResult == ResolverFormatError || e.DNSResult == ResolverNotImplemented ||
				e.DNSResult == ResolverOtherError || e.DNSResult == ResolverReferral || e.DNSResult == ResolverTruncated || e.DNSResult == ResolverUnclassified
			reply = reply || responded
			dnsReply = dnsReply || responded
			dnsIssue = dnsIssue || e.State == StateIssueObserved
		default:
			responded := e.Successes > 0
			if e.Method == MethodHTTPS {
				responded = e.HTTPStatus != 0
			}
			reply = reply || responded
			if e.Layer == LayerGateway {
				icmpMiss = icmpMiss || !passed
				icmpReply = icmpReply || e.Successes > 0
			}
			if e.Layer == LayerExternal {
				externalMiss = externalMiss || !passed
				externalReply = externalReply || responded
			}
		}
		if !passed {
			allPassed = false
		}
		if e.State == StateIssueObserved {
			failureLayers[e.Layer] = true
		}
	}
	out.Confidence = ConfidenceLimited
	switch {
	case linkDown && reply:
		out.Conclusion = "mixed-link-evidence"
		out.Summary = "The interface reported down while nearby selected checks recorded responses."
		out.NextStep = "Review the original timestamps and collection sources for a transition or path difference. Do not choose one observation as proof of the whole connection."
	case linkDown && len(failureLayers) > 1:
		out.Conclusion = "local-link-issue-with-other-failures"
		out.Summary = "The interface reported down and another selected check also recorded a problem nearby in time."
		out.NextStep = "Inspect this device's local connection first, then compare separately authorized evidence. The observations do not prove the same cause or another device's state."
	case dnsIssue && (icmpReply || externalReply):
		out.Conclusion = "dns-query-issue-with-responses"
		out.Summary = "A selected DNS query differed from its expectation or failed, while another layer recorded responses nearby in time."
		out.NextStep = "Inspect the recorded DNS result and query expectation. Resolver policy, filtering or upstream behavior remain possible, not proven causes; other replies do not prove the resolver path worked."
	case icmpMiss && (dnsReply || externalReply):
		out.Conclusion = "icmp-misses-with-responses"
		out.Summary = "The selected ICMP target missed replies while another selected check recorded a response nearby in time."
		out.NextStep = "Review target and protocol behavior, including ICMP filtering, before inferring a wider connectivity failure. The target's gateway role is unverified."
	case externalMiss && (dnsReply || icmpReply):
		out.Conclusion = "external-check-issue-with-responses"
		out.Summary = "An external target check had a problem while another selected check recorded responses nearby in time."
		out.NextStep = "Review that target and protocol, then corroborate with other authorized evidence. This does not diagnose an ISP outage or captive portal."
	case len(failureLayers) > 1:
		out.Conclusion = "problems-across-selected-layers"
		out.Summary = "Checks in more than one selected layer recorded problems nearby in time."
		out.NextStep = "Inspect each original result and local binding. Shared dependencies, target behavior and collection differences prevent an internet-outage or root-cause verdict."
	case allPassed:
		out.Conclusion = "selected-checks-matched"
		out.Summary = "The compared checks met their selected expectations during this evidence window."
		out.NextStep = "Keep the conclusion limited to these checks and their original times. This is not proof of general internet availability, security or coverage."
	default:
		out.Conclusion = "mixed-or-limited-evidence"
		out.Summary = "Comparable checks provide mixed or limited evidence without a supported explanation for a wider problem."
		out.NextStep = "Inspect individual observations, including incomplete DNS classifications, before collecting any separately authorized follow-up."
	}
	return out, nil
}

func validateCorroboration(in CorroborationInput) error {
	invalid := func() error { return fmt.Errorf("quality comparison context is invalid") }
	a, b := in.Network, in.Resolvers
	if !validToken(in.NetworkDeviceID) || in.NetworkDeviceID != in.ResolverDeviceID ||
		a.Observer.ScopeID != b.Observer.ScopeID || a.Observer.InterfaceName != b.Observer.InterfaceName || a.Observer.InterfaceIndex != b.Observer.InterfaceIndex ||
		!a.AsOf.Equal(b.AsOf) || !a.WindowStart.Equal(b.WindowStart) || a.Freshness != b.Freshness ||
		(in.Family != FamilyIPv4 && in.Family != FamilyIPv6) || in.MaxCompletionSkew < time.Second || in.MaxCompletionSkew > MaxCorroborationSkew ||
		len(a.Targets)+len(b.Selections) > MaxTargets || len(a.Measurements)+len(b.Measurements) > MaxMeasurements {
		return invalid()
	}
	if !resolverTime(a.AsOf) || !resolverTime(a.WindowStart) {
		return invalid()
	}
	if err := ValidateSnapshot(a); err != nil {
		return invalid()
	}
	if err := ValidateResolverSnapshot(b); err != nil {
		return invalid()
	}
	if in.HTTPS == nil {
		if in.HTTPSDeviceID != "" {
			return invalid()
		}
	} else {
		h := in.HTTPS
		if in.HTTPSDeviceID != in.NetworkDeviceID || h.Observer.ScopeID != a.Observer.ScopeID || h.Observer.InterfaceName != a.Observer.InterfaceName || h.Observer.InterfaceIndex != a.Observer.InterfaceIndex ||
			!h.AsOf.Equal(a.AsOf) || !h.WindowStart.Equal(a.WindowStart) || h.Freshness != a.Freshness ||
			len(a.Targets)+len(b.Selections)+len(h.Selections) > MaxTargets || len(a.Measurements)+len(b.Measurements)+len(h.Measurements) > MaxMeasurements || ValidateHTTPSSnapshot(*h) != nil {
			return invalid()
		}
	}
	ids := map[string]bool{}
	externalRefs := map[string]bool{}
	for _, t := range a.Targets {
		if t.Layer == LayerExternal {
			externalRefs[t.ID] = true
		}
		if t.Layer == LayerDNS || t.Method == MethodHTTPS {
			return invalid()
		}
	} // DNS and HTTPS must retain specialized response/expectation semantics.
	if in.HTTPS != nil {
		for _, selection := range in.HTTPS.Selections {
			if externalRefs[selection.ID] {
				return invalid()
			}
		}
	}
	for _, m := range a.Measurements {
		if !resolverTime(m.StartedAt) || !resolverTime(m.CompletedAt) {
			return invalid()
		}
		ids[m.ID] = true
	}
	for _, m := range b.Measurements {
		if ids[m.ID] {
			return invalid()
		}
		ids[m.ID] = true
	}
	if in.HTTPS != nil {
		for _, m := range in.HTTPS.Measurements {
			if ids[m.ID] {
				return invalid()
			}
			ids[m.ID] = true
		}
	}
	return nil
}

// Scan the bounded raw window, not just latest-per-target samples: a newer
// successful check cannot erase a gap that invalidates another target's older
// evidence. These are explicit runtime gaps, never inferred from missing data.
func latestDiscontinuity(in CorroborationInput) (string, time.Time, GapReason) {
	var id string
	var at time.Time
	var gap GapReason
	consider := func(mid string, end time.Time, reason GapReason) {
		if reason != GapSleep && reason != GapOffline && reason != GapNetworkChanged {
			return
		}
		if end.After(at) || (end.Equal(at) && mid < id) {
			id, at, gap = mid, end, reason
		}
	}
	// Runtime sleep/offline/binding gaps concern this shared observation point,
	// not just the address family of the check that happened to record the gap.
	for _, m := range in.Network.Measurements {
		consider(m.ID, m.CompletedAt, m.Gap)
	}
	for _, m := range in.Resolvers.Measurements {
		consider(m.ID, m.CompletedAt, m.Gap)
	}
	if in.HTTPS != nil {
		for _, m := range in.HTTPS.Measurements {
			consider(m.ID, m.CompletedAt, m.Gap)
		}
	}
	return id, at, gap
}
