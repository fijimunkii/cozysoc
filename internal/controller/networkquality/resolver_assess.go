package networkquality

import "time"

// ResolverResult interprets the selected evidence at measurement time. It never
// asserts all-DNS health, endpoint identity, DNSSEC validation or connectivity.
type ResolverResult string

const (
	ResolverUnknown          ResolverResult = "unknown"
	ResolverNotMeasured      ResolverResult = "not-measured"
	ResolverIncomplete       ResolverResult = "incomplete"
	ResolverTimeout          ResolverResult = "timeout"
	ResolverTransportFailure ResolverResult = "transport-failure"
	ResolverAnswer           ResolverResult = "answer"
	ResolverNXDOMAIN         ResolverResult = "nxdomain"
	ResolverNoData           ResolverResult = "no-data"
	ResolverRefused          ResolverResult = "refused"
	ResolverServerFailure    ResolverResult = "server-failure"
	ResolverFormatError      ResolverResult = "format-error"
	ResolverNotImplemented   ResolverResult = "not-implemented"
	ResolverOtherError       ResolverResult = "other-response-error"
	ResolverReferral         ResolverResult = "referral"
	ResolverTruncated        ResolverResult = "truncated"
	ResolverUnclassified     ResolverResult = "unclassified-response"
)

type ResolverAssessment struct {
	Selection    ResolverSelection
	State        State
	Confidence   Confidence
	Result       ResolverResult       // Interpretation of Evidence, even when State is stale.
	Evidence     *ResolverMeasurement // Defensive copy, including historical reply timing.
	FreshUntil   time.Time
	ResponseTime *time.Duration // Recent matched-response timing only; never packet loss.
	Summary      string
	NextStep     string
}

type ResolverReport struct {
	Observer    Observer
	AsOf        time.Time
	WindowStart time.Time
	Checks      []ResolverAssessment
	Limitations []string
}

// AssessResolvers selects the latest evidence for each explicitly supplied
// selection. It does not resolve names, parse packets, follow referrals, retry,
// discover resolvers or grant send authority. A newer gap replaces older success.
func AssessResolvers(s ResolverSnapshot) (ResolverReport, error) {
	if err := ValidateResolverSnapshot(s); err != nil {
		return ResolverReport{}, err
	}
	latest := make(map[string]ResolverMeasurement, len(s.Selections))
	for _, m := range s.Measurements {
		old, exists := latest[m.Selection.ID]
		if !exists || m.CompletedAt.After(old.CompletedAt) {
			latest[m.Selection.ID] = m
		}
	}
	out := ResolverReport{Observer: s.Observer, AsOf: s.AsOf, WindowStart: s.WindowStart, Checks: make([]ResolverAssessment, 0, len(s.Selections)),
		Limitations: []string{
			"Results describe only the selected resolver, query, transport, address family and observing interface at measurement time.",
			"A DNS error reply is a response, not packet loss. A timeout or one resolver failure does not establish an internet outage or all-DNS failure.",
			"Answers may reflect cache, split DNS, filtering or resolver policy; neither positive nor negative replies prove DNSSEC validation or data correctness.",
			"This evidence is separate from security findings and monitoring coverage. Configuration references and evidence do not authorize traffic or network changes.",
		}}
	for _, selection := range s.Selections {
		check := ResolverAssessment{Selection: selection, State: StateUnknown, Confidence: ConfidenceUnknown, Result: ResolverUnknown,
			Summary: "No measurement is available for this selected resolver query.", NextStep: "Review the selected resolver and query before collecting separately authorized evidence."}
		if m, exists := latest[selection.ID]; exists {
			check = assessResolverMeasurement(m, s.AsOf, s.Freshness)
		}
		out.Checks = append(out.Checks, check)
	}
	return out, nil
}

func copyResolverMeasurement(m ResolverMeasurement) *ResolverMeasurement {
	if m.Reply != nil {
		reply := *m.Reply
		m.Reply = &reply
	}
	if m.ResponseTime != nil {
		latency := *m.ResponseTime
		m.ResponseTime = &latency
	}
	return &m
}

func assessResolverMeasurement(m ResolverMeasurement, asOf time.Time, freshness time.Duration) ResolverAssessment {
	result := resolverResult(m)
	out := ResolverAssessment{Selection: m.Selection, State: StateStale, Confidence: ConfidenceUnknown, Result: result,
		Evidence: copyResolverMeasurement(m), FreshUntil: m.CompletedAt.Add(freshness),
		Summary: "This resolver query has only historical evidence.", NextStep: "Compare evidence from the same time; any new query needs separate authorization."}
	if !out.FreshUntil.After(asOf) {
		return out
	}
	if m.Exchange == DNSNotMeasured {
		out.State = StateNotMeasured
		out.Summary, out.NextStep = gapCopy(m.Gap)
		return out
	}
	out.State = StateIssueObserved
	out.Confidence = ConfidenceLimited
	out.Summary, out.NextStep = resolverResultCopy(result)
	if result == ResolverIncomplete || result == ResolverUnclassified || result == ResolverTruncated || result == ResolverReferral {
		out.State = StateUnknown
	}
	if result == ResolverIncomplete {
		out.Confidence = ConfidenceUnknown
	}
	if m.ResponseTime != nil {
		latency := *m.ResponseTime
		out.ResponseTime = &latency
	}
	if result == ResolverAnswer || result == ResolverNXDOMAIN || result == ResolverNoData {
		expected := (result == ResolverAnswer && m.Selection.Expect == DNSExpectAnswer) ||
			(result == ResolverNXDOMAIN && m.Selection.Expect == DNSExpectNXDOMAIN) || (result == ResolverNoData && m.Selection.Expect == DNSExpectNoData)
		if expected {
			out.State = StateSucceeded
			out.Summary += " This matches the selected query expectation."
		} else {
			out.Summary += " This differs from the selected query expectation."
			out.NextStep = "Review the expected query result and resolver policy. A differing answer does not prove a broken resolver or an internet outage."
		}
	}
	return out
}

func resolverResult(m ResolverMeasurement) ResolverResult {
	switch m.Exchange {
	case DNSNotMeasured:
		return ResolverNotMeasured
	case DNSIncomplete:
		return ResolverIncomplete
	case DNSTimeout:
		return ResolverTimeout
	case DNSTransportError:
		return ResolverTransportFailure
	}
	if m.Reply.Truncated {
		return ResolverTruncated
	}
	switch m.Reply.RCode {
	case 1:
		return ResolverFormatError
	case 2:
		return ResolverServerFailure
	case 3:
		return ResolverNXDOMAIN
	case 4:
		return ResolverNotImplemented
	case 5:
		return ResolverRefused
	case 0:
		switch m.Reply.Answer {
		case DNSAnswerPresent:
			return ResolverAnswer
		case DNSAnswerNoData:
			return ResolverNoData
		case DNSAnswerReferral:
			return ResolverReferral
		default:
			return ResolverUnclassified
		}
	default:
		return ResolverOtherError
	}
}

func resolverResultCopy(result ResolverResult) (string, string) {
	switch result {
	case ResolverAnswer:
		return "The selected resolver returned an answer for the selected question.", "This response is limited to that query; it does not establish all-DNS health, address reachability or security."
	case ResolverNXDOMAIN:
		return "The selected resolver reported that the queried name does not exist (NXDOMAIN).", "A negative reply is still a response. Review the query expectation and resolver policy before treating it as a problem."
	case ResolverNoData:
		return "The selected resolver returned no data of the requested type (NODATA).", "This is distinct from NXDOMAIN and a timeout. Review the requested record type and query expectation."
	case ResolverRefused:
		return "The selected resolver refused this query (REFUSED).", "Review that resolver's access policy and the selected query; do not infer that the internet is down."
	case ResolverServerFailure:
		return "The selected resolver reported a server failure for this query (SERVFAIL).", "Corroborate with other evidence. The response alone does not identify upstream, validation or connectivity as the cause."
	case ResolverFormatError:
		return "The selected resolver reported a query format error (FORMERR).", "Review collector/query compatibility; a format error is not a connectivity diagnosis."
	case ResolverNotImplemented:
		return "The selected resolver reported that this query operation is not implemented (NOTIMP).", "Review support for the selected query before considering another authorized measurement."
	case ResolverOtherError:
		return "The selected resolver returned another DNS error code.", "Inspect the retained response code and collector support; it is not a packet-loss measurement."
	case ResolverTimeout:
		return "No matched DNS reply was observed before this accepted request's timeout.", "Compare the selected resolver, transport and local-path evidence. One timeout does not prove an internet outage."
	case ResolverTransportFailure:
		return "The DNS exchange encountered a transport error.", "Review the recorded request state and local-path evidence. Transport failure is distinct from a DNS refusal response."
	case ResolverIncomplete:
		return "The DNS exchange is incomplete; no final query result is available.", "An unsent or uncertain request is not a timeout. Do not automatically retry uncertain work."
	case ResolverTruncated:
		return "A matched DNS response was truncated; the lookup result remains unknown.", "This contract does not retry over TCP. Any fallback must be explicitly bounded and authorized."
	case ResolverReferral:
		return "The selected resolver returned a referral rather than a final answer.", "No referral is followed here. Review resolver/query configuration without contacting additional servers automatically."
	default:
		return "A matched DNS response could not be classified as a final answer.", "Preserve the response as incomplete lookup evidence; do not infer NODATA from an empty answer section alone."
	}
}
