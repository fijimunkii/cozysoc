package networkquality

import "time"

type HTTPSResult string

const (
	HTTPSUnknown           HTTPSResult = "unknown"
	HTTPSUnmeasured        HTTPSResult = "not-measured"
	HTTPSUnfinished        HTTPSResult = "incomplete"
	HTTPSConnectionFailure HTTPSResult = "connection-failure"
	HTTPSTransportFailure  HTTPSResult = "transport-failure"
	HTTPSTLSFailure        HTTPSResult = "tls-failure"
	HTTPSProtocolFailure   HTTPSResult = "protocol-failure"
	HTTPSTimedOut          HTTPSResult = "timeout"
	HTTPSStatusResponse    HTTPSResult = "status-response"
	HTTPSRedirectResponse  HTTPSResult = "redirect-response"
)

type HTTPSAssessment struct {
	Selection          HTTPSSelection
	State              State
	Confidence         Confidence
	Result             HTTPSResult // Historical interpretation remains available when stale.
	Evidence           *HTTPSMeasurement
	FreshUntil         time.Time
	ExpectationMatched *bool          // Nil without a final status; preserved even when stale.
	ResponseTime       *time.Duration // Fresh header-response timing only; never packet loss.
	Summary, NextStep  string
}

type HTTPSReport struct {
	Observer          Observer
	AsOf, WindowStart time.Time
	Checks            []HTTPSAssessment
	Limitations       []string
}

// AssessHTTPS performs no I/O. Latest evidence is selected before freshness checks;
// an incomplete exchange or explicit gap cannot be hidden by an older success.
func AssessHTTPS(s HTTPSSnapshot) (HTTPSReport, error) {
	if err := ValidateHTTPSSnapshot(s); err != nil {
		return HTTPSReport{}, err
	}
	latest := make(map[string]HTTPSMeasurement, len(s.Selections))
	for _, m := range s.Measurements {
		if old, exists := latest[m.Selection.ID]; !exists || m.CompletedAt.After(old.CompletedAt) {
			latest[m.Selection.ID] = m
		}
	}
	out := HTTPSReport{Observer: s.Observer, AsOf: s.AsOf, WindowStart: s.WindowStart, Checks: make([]HTTPSAssessment, 0, len(s.Selections)), Limitations: []string{
		"Evidence describes one selected HTTPS endpoint, request, address family and observing interface at the original time. A matching status does not prove body correctness or general internet availability.",
		"An HTTP error or redirect is a response, not packet loss. A TLS failure or redirect alone does not establish a captive portal, interception, an ISP outage or a security finding.",
		"No DNS resolution, redirect, retry or alternate destination is performed here. HTTP request not-sent does not mean no connection or TLS traffic was sent.",
		"Configuration references are not consent. A collector must separately disclose the exact destination, TLS identity, request, data budget and privacy impact, and verify the enrolled binding before an explicitly approved check.",
	}}
	for _, selection := range s.Selections {
		check := HTTPSAssessment{Selection: selection, State: StateUnknown, Confidence: ConfidenceUnknown, Result: HTTPSUnknown,
			Summary: "No measurement is available for this selected HTTPS check.", NextStep: "Review the explicit target and privacy disclosure before collecting separately authorized evidence."}
		if m, exists := latest[selection.ID]; exists {
			check = assessHTTPSMeasurement(m, s.AsOf, s.Freshness)
		}
		out.Checks = append(out.Checks, check)
	}
	return out, nil
}

func assessHTTPSMeasurement(m HTTPSMeasurement, asOf time.Time, freshness time.Duration) HTTPSAssessment {
	if m.ResponseTime != nil {
		latency := *m.ResponseTime
		m.ResponseTime = &latency
	}
	out := HTTPSAssessment{Selection: m.Selection, State: StateStale, Confidence: ConfidenceUnknown, Result: httpsResult(m), Evidence: &m, FreshUntil: m.CompletedAt.Add(freshness),
		Summary: "This HTTPS check has only historical evidence.", NextStep: "Compare observations from the same time; any new external request needs separate authorization."}
	if m.Exchange == HTTPSResponseReceived {
		matched := m.StatusCode == m.Selection.ExpectedStatus
		out.ExpectationMatched = &matched
	}
	if !out.FreshUntil.After(asOf) {
		return out
	}
	if m.Exchange == HTTPSNotMeasured {
		out.State = StateNotMeasured
		out.Summary, out.NextStep = gapCopy(m.Gap)
		// The generic source-unavailable copy suggests a retry without explicit consent.
		out.NextStep = "Review the recorded gap and current enrollment. Any external check still needs explicit destination disclosure and separate approval."
		return out
	}
	out.State, out.Confidence = StateIssueObserved, ConfidenceLimited
	out.Summary, out.NextStep = httpsResultCopy(out.Result, m.Stage)
	if m.Exchange == HTTPSIncomplete {
		out.State, out.Confidence = StateUnknown, ConfidenceUnknown
	}
	if out.ExpectationMatched != nil {
		if *out.ExpectationMatched {
			out.State = StateSucceeded
			out.Summary += " This matches the selected status expectation."
		} else {
			out.Summary += " This differs from the selected status expectation."
		}
	}
	if m.ResponseTime != nil {
		latency := *m.ResponseTime
		out.ResponseTime = &latency
	}
	return out
}
func httpsResult(m HTTPSMeasurement) HTTPSResult {
	switch m.Exchange {
	case HTTPSNotMeasured:
		return HTTPSUnmeasured
	case HTTPSIncomplete:
		return HTTPSUnfinished
	case HTTPSConnectError:
		return HTTPSConnectionFailure
	case HTTPSTransportError:
		return HTTPSTransportFailure
	case HTTPSTLSError:
		return HTTPSTLSFailure
	case HTTPSProtocolError:
		return HTTPSProtocolFailure
	case HTTPSTimeout:
		return HTTPSTimedOut
	default:
		switch m.StatusCode {
		case 301, 302, 303, 307, 308:
			return HTTPSRedirectResponse
		}
		return HTTPSStatusResponse
	}
}
func httpsResultCopy(result HTTPSResult, stage HTTPSStage) (string, string) {
	switch result {
	case HTTPSConnectionFailure:
		return "The selected endpoint connection failed before an HTTP request.", "Review the selected address and local-path evidence. This does not prove that the internet or the endpoint is generally unavailable."
	case HTTPSTransportFailure:
		return "The selected exchange encountered a transport error after connecting.", "Inspect the recorded stage and HTTP request state. A connection reset is distinct from a TLS verification failure or an HTTP error response; uncertain work must not be retried automatically."
	case HTTPSTLSFailure:
		return "The selected endpoint's TLS exchange failed before an HTTP request.", "Review the TLS identity, local clock and protocol compatibility. Do not bypass certificate verification or infer interception or a captive portal from this result alone."
	case HTTPSProtocolFailure:
		return "The selected exchange did not produce a usable final HTTP response header.", "Review collector limits and server compatibility. Invalid or oversized response metadata does not establish an internet outage."
	case HTTPSTimedOut:
		if stage == HTTPSConnect {
			return "The selected endpoint connection timed out.", "Compare the original address and local-path evidence. One timeout is not an internet-outage verdict."
		}
		if stage == HTTPSTLS {
			return "The selected endpoint's TLS exchange timed out.", "Review the recorded stage before inferring a cause. No HTTP request was sent, but connection and TLS traffic may have been sent."
		}
		return "The selected HTTPS request stage timed out without a final response header.", "Inspect the recorded HTTP request state. An uncertain write must not be automatically retried; this timeout is not packet loss."
	case HTTPSUnfinished:
		return "The HTTPS check ended without a final result.", "Preserve the stage and request state. Cancellation or uncertain work is not a completed timeout and does not authorize a retry."
	case HTTPSRedirectResponse:
		return "The selected HTTPS exchange returned a redirect status.", "No redirect is followed. Review the selected expectation; a redirect alone does not prove a captive portal or authorize contact with another destination."
	default:
		return "The selected HTTPS exchange returned a final status.", "Compare the recorded status with the selected expectation. Error statuses are still responses; header receipt does not establish body correctness or general connectivity."
	}
}
