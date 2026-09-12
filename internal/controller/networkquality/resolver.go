package networkquality

import (
	"fmt"
	"time"
)

// ResolverSelection is an immutable configuration reference, not an executable
// destination or authority. ResolverID and QueryID must change when their pinned
// endpoint/name configuration changes. No resolver or query is chosen by default.
// Family describes the resolver transport, independently of the A/AAAA question.
type ResolverSelection struct {
	ID         string
	ResolverID string
	QueryID    string
	Family     AddressFamily
	Transport  DNSTransport
	QueryType  DNSQueryType
	Expect     DNSExpectation
}

type DNSTransport string

const (
	DNSUDP DNSTransport = "udp"
	DNSTCP DNSTransport = "tcp"
)

type DNSQueryType string

const (
	DNSQueryA    DNSQueryType = "A"
	DNSQueryAAAA DNSQueryType = "AAAA"
)

type DNSExpectation string

const (
	DNSExpectAnswer   DNSExpectation = "answer"
	DNSExpectNXDOMAIN DNSExpectation = "nxdomain"
	DNSExpectNoData   DNSExpectation = "no-data"
)

type DNSExchangeState string

const (
	DNSResponseReceived DNSExchangeState = "response-received"
	DNSTimeout          DNSExchangeState = "timeout"
	DNSTransportError   DNSExchangeState = "transport-error"
	DNSIncomplete       DNSExchangeState = "incomplete"
	DNSNotMeasured      DNSExchangeState = "not-measured"
)

// Request state is evidence about one logical DNS request. An uncertain send is
// never promoted to a timeout or retried by this contract.
type DNSRequestState string

const (
	DNSRequestNotSent   DNSRequestState = "not-sent"
	DNSRequestAccepted  DNSRequestState = "accepted"
	DNSRequestUncertain DNSRequestState = "uncertain"
)

type DNSAnswerKind string

const (
	DNSAnswerPresent      DNSAnswerKind = "answer"
	DNSAnswerNoData       DNSAnswerKind = "no-data"
	DNSAnswerReferral     DNSAnswerKind = "referral"
	DNSAnswerUnclassified DNSAnswerKind = "unclassified"
)

// DNSReply is normalized evidence from an already matched response, never a raw
// packet parser. A collector must validate sender, transaction, question and
// message structure before creating it. Positive answers must satisfy the
// selected question; NOERROR alone does not prove that. NODATA requires content
// classification (RFC 2308), not merely an empty answer section. Truncated and
// nonzero-RCODE replies cannot carry a positive/negative answer classification.
// RCode includes the 12-bit extended value when present (RFC 6891).
type DNSReply struct {
	RCode     int
	Truncated bool
	Answer    DNSAnswerKind
}

// ResolverMeasurement describes at most one logical DNS exchange. ResponseTime
// times a matched response, including error replies; it is not a successful-
// lookup metric. A gap means no request was sent. Incomplete/uncertain work stays
// distinct from a completed timeout. There are no raw names, answers or errors.
type ResolverMeasurement struct {
	ID           string
	Selection    ResolverSelection
	Observer     Observer
	StartedAt    time.Time
	CompletedAt  time.Time
	Exchange     DNSExchangeState
	Request      DNSRequestState
	Gap          GapReason
	Reply        *DNSReply
	ResponseTime *time.Duration
}

type ResolverSnapshot struct {
	Observer     Observer
	AsOf         time.Time
	WindowStart  time.Time
	Freshness    time.Duration
	Selections   []ResolverSelection
	Measurements []ResolverMeasurement
}

func resolverTime(at time.Time) bool {
	return !at.IsZero() && time.Unix(0, at.UnixNano()).Equal(at)
}

func validResolverSelection(s ResolverSelection) bool {
	return validToken(s.ID) && validToken(s.ResolverID) && validToken(s.QueryID) &&
		(s.Family == FamilyIPv4 || s.Family == FamilyIPv6) && (s.Transport == DNSUDP || s.Transport == DNSTCP) &&
		(s.QueryType == DNSQueryA || s.QueryType == DNSQueryAAAA) &&
		(s.Expect == DNSExpectAnswer || s.Expect == DNSExpectNXDOMAIN || s.Expect == DNSExpectNoData)
}

// ValidateResolverSelection validates an immutable reference, not execution authority.
func ValidateResolverSelection(s ResolverSelection) error {
	if !validResolverSelection(s) {
		return fmt.Errorf("resolver selection is invalid")
	}
	return nil
}

// ValidateResolverSnapshot validates normalized evidence, not network authority,
// wire authenticity or DNSSEC. Bounds apply before map allocation and iteration.
func ValidateResolverSnapshot(s ResolverSnapshot) error {
	if !validObserver(s.Observer) || s.Observer.InterfaceIndex > 2147483647 || !resolverTime(s.AsOf) || !resolverTime(s.WindowStart) ||
		s.WindowStart.After(s.AsOf) || s.AsOf.Sub(s.WindowStart) > MaxWindow ||
		s.Freshness < time.Second || s.Freshness > MaxFreshness || !resolverTime(s.AsOf.Add(s.Freshness)) ||
		len(s.Selections) > MaxTargets || len(s.Measurements) > MaxMeasurements {
		return fmt.Errorf("resolver assessment context or bounds are invalid")
	}
	selections := make(map[string]ResolverSelection, len(s.Selections))
	for _, selection := range s.Selections {
		if !validResolverSelection(selection) {
			return fmt.Errorf("resolver selection is invalid")
		}
		if _, exists := selections[selection.ID]; exists {
			return fmt.Errorf("duplicate resolver selection")
		}
		selections[selection.ID] = selection
	}
	seen := make(map[string]bool, len(s.Measurements))
	ends := make(map[string]map[time.Time]bool, len(selections))
	for _, m := range s.Measurements {
		selection, exists := selections[m.Selection.ID]
		if !exists || selection != m.Selection || m.Observer != s.Observer || !validToken(m.ID) || seen[m.ID] {
			return fmt.Errorf("resolver evidence identity or context is invalid")
		}
		seen[m.ID] = true
		if !resolverTime(m.StartedAt) || !resolverTime(m.CompletedAt) || m.StartedAt.Before(s.WindowStart) ||
			m.CompletedAt.Before(m.StartedAt) || m.CompletedAt.After(s.AsOf) {
			return fmt.Errorf("resolver evidence timestamps are invalid")
		}
		end := m.CompletedAt.Round(0).UTC()
		if ends[selection.ID] == nil {
			ends[selection.ID] = make(map[time.Time]bool)
		}
		if ends[selection.ID][end] {
			return fmt.Errorf("resolver evidence has ambiguous simultaneous results")
		}
		ends[selection.ID][end] = true
		if err := validateResolverMeasurement(m); err != nil {
			return err
		}
	}
	return nil
}

func validateResolverMeasurement(m ResolverMeasurement) error {
	invalid := func() error { return fmt.Errorf("resolver exchange evidence is inconsistent") }
	if m.Request != DNSRequestNotSent && m.Request != DNSRequestAccepted && m.Request != DNSRequestUncertain {
		return invalid()
	}
	if m.Exchange == DNSNotMeasured {
		if m.Request != DNSRequestNotSent || m.Reply != nil || m.ResponseTime != nil {
			return invalid()
		}
		switch m.Gap {
		case GapPermission, GapUnsupported, GapSourceUnavailable, GapDisabled, GapNotConfigured, GapSleep, GapOffline, GapNetworkChanged:
			return nil
		default:
			return invalid()
		}
	}
	if m.Gap != GapNone || m.CompletedAt.Sub(m.StartedAt) > MaxCheckTime {
		return invalid()
	}
	if m.Exchange != DNSResponseReceived {
		if m.Reply != nil || m.ResponseTime != nil {
			return invalid()
		}
		switch m.Exchange {
		case DNSTimeout:
			if m.Request != DNSRequestAccepted || !m.CompletedAt.After(m.StartedAt) {
				return invalid()
			}
		case DNSTransportError, DNSIncomplete:
		default:
			return invalid()
		}
		return nil
	}
	if m.Request != DNSRequestAccepted || m.Reply == nil {
		return invalid()
	}
	reply := m.Reply
	if reply.RCode < 0 || reply.RCode > 4095 {
		return invalid()
	}
	if reply.Truncated || reply.RCode != 0 {
		if reply.Answer != "" {
			return invalid()
		}
	} else {
		switch reply.Answer {
		case DNSAnswerPresent, DNSAnswerNoData, DNSAnswerReferral, DNSAnswerUnclassified:
		default:
			return invalid()
		}
	}
	if m.ResponseTime != nil && (*m.ResponseTime < 0 || *m.ResponseTime > m.CompletedAt.Sub(m.StartedAt)) {
		return invalid()
	}
	return nil
}
