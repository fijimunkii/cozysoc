package networkquality

import (
	"fmt"
	"time"
)

// HTTPSSelection references immutable, explicitly selected configuration. No
// address, URL, SNI name, credentials or request bytes belong in evidence. The
// endpoint reference must rotate when the pinned address or TLS identity changes;
// the request reference must rotate when method/path/expectation changes.
// These references grant no authority and imply no default external service.
type HTTPSSelection struct {
	ID             string
	EndpointID     string
	RequestID      string
	Family         AddressFamily
	Method         string // GET or HEAD; no request body in this evidence profile.
	ExpectedStatus int    // One explicitly selected final HTTP status, including errors.
}

type HTTPSStage string

const (
	HTTPSConnect HTTPSStage = "connect"
	HTTPSTLS     HTTPSStage = "tls"
	HTTPSRequest HTTPSStage = "request" // Reached only after verifying the selected TLS identity.
)

type HTTPSExchange string

const (
	HTTPSResponseReceived HTTPSExchange = "response-received"
	HTTPSConnectError     HTTPSExchange = "connect-error"
	HTTPSTransportError   HTTPSExchange = "transport-error"
	HTTPSTLSError         HTTPSExchange = "tls-error"
	HTTPSProtocolError    HTTPSExchange = "protocol-error"
	HTTPSTimeout          HTTPSExchange = "timeout"
	HTTPSIncomplete       HTTPSExchange = "incomplete"
	HTTPSNotMeasured      HTTPSExchange = "not-measured"
)

type HTTPSRequestState string

const (
	HTTPSRequestNotSent   HTTPSRequestState = "not-sent"
	HTTPSRequestAccepted  HTTPSRequestState = "accepted"
	HTTPSRequestUncertain HTTPSRequestState = "uncertain"
)

// HTTPSMeasurement represents one connection and at most one logical request.
// Request state concerns HTTP bytes, NOT whether TCP/TLS traffic was sent.
// Stage=request requires collector evidence of successful TLS identity validation;
// never populate it after disabling verification. ResponseReceived requires a
// bounded, parsed final response header from that exchange. Informational replies,
// redirects followed to another resource and body-completion claims are excluded.
// ResponseTime runs from StartedAt through receipt of the final response header,
// including connection/TLS time. Raw headers, locations, bodies and errors are absent.
type HTTPSMeasurement struct {
	ID                     string
	Selection              HTTPSSelection
	Observer               Observer
	StartedAt, CompletedAt time.Time
	Stage                  HTTPSStage // Empty only when no measurement was attempted.
	Exchange               HTTPSExchange
	Request                HTTPSRequestState
	Gap                    GapReason
	StatusCode             int // Zero unless a final response header was received.
	ResponseTime           *time.Duration
}

type HTTPSSnapshot struct {
	Observer          Observer
	AsOf, WindowStart time.Time
	Freshness         time.Duration
	Selections        []HTTPSSelection
	Measurements      []HTTPSMeasurement
}

func ValidateHTTPSSelection(s HTTPSSelection) error {
	if !validToken(s.ID) || !validToken(s.EndpointID) || !validToken(s.RequestID) ||
		(s.Family != FamilyIPv4 && s.Family != FamilyIPv6) || (s.Method != "GET" && s.Method != "HEAD") || s.ExpectedStatus < 200 || s.ExpectedStatus > 599 {
		return fmt.Errorf("HTTPS selection is invalid")
	}
	return nil
}

// ValidateHTTPSSnapshot validates bounded normalized claims, not wire authenticity,
// destination eligibility, routing, TLS verification itself or send permission.
func ValidateHTTPSSnapshot(s HTTPSSnapshot) error {
	if !validObserver(s.Observer) || s.Observer.InterfaceIndex > 2147483647 || !resolverTime(s.AsOf) || !resolverTime(s.WindowStart) ||
		s.WindowStart.After(s.AsOf) || s.AsOf.Sub(s.WindowStart) > MaxWindow || s.Freshness < time.Second || s.Freshness > MaxFreshness ||
		!resolverTime(s.AsOf.Add(s.Freshness)) || len(s.Selections) > MaxTargets || len(s.Measurements) > MaxMeasurements {
		return fmt.Errorf("HTTPS assessment context or bounds are invalid")
	}
	selections := make(map[string]HTTPSSelection, len(s.Selections))
	for _, selection := range s.Selections {
		if ValidateHTTPSSelection(selection) != nil {
			return fmt.Errorf("HTTPS selection is invalid")
		}
		if _, exists := selections[selection.ID]; exists {
			return fmt.Errorf("duplicate HTTPS selection")
		}
		selections[selection.ID] = selection
	}
	seen := make(map[string]bool, len(s.Measurements))
	ends := make(map[string]map[time.Time]bool, len(selections))
	for _, m := range s.Measurements {
		selection, exists := selections[m.Selection.ID]
		if !exists || selection != m.Selection || m.Observer != s.Observer || !validToken(m.ID) || seen[m.ID] {
			return fmt.Errorf("HTTPS evidence identity or context is invalid")
		}
		seen[m.ID] = true
		if !resolverTime(m.StartedAt) || !resolverTime(m.CompletedAt) || m.StartedAt.Before(s.WindowStart) || m.CompletedAt.Before(m.StartedAt) || m.CompletedAt.After(s.AsOf) {
			return fmt.Errorf("HTTPS evidence timestamps are invalid")
		}
		end := m.CompletedAt.Round(0).UTC()
		if ends[selection.ID] == nil {
			ends[selection.ID] = make(map[time.Time]bool)
		}
		if ends[selection.ID][end] {
			return fmt.Errorf("HTTPS evidence has ambiguous simultaneous results")
		}
		ends[selection.ID][end] = true
		if err := validateHTTPSMeasurement(m); err != nil {
			return err
		}
	}
	return nil
}

func validateHTTPSMeasurement(m HTTPSMeasurement) error {
	invalid := func() error { return fmt.Errorf("HTTPS exchange evidence is inconsistent") }
	if m.Request != HTTPSRequestNotSent && m.Request != HTTPSRequestAccepted && m.Request != HTTPSRequestUncertain {
		return invalid()
	}
	if m.Exchange == HTTPSNotMeasured {
		if m.Stage != "" || m.Request != HTTPSRequestNotSent || m.StatusCode != 0 || m.ResponseTime != nil {
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
	switch m.Stage {
	case HTTPSConnect, HTTPSTLS:
		if m.Request != HTTPSRequestNotSent {
			return invalid()
		}
	case HTTPSRequest:
	default:
		return invalid()
	}
	if m.Exchange == HTTPSResponseReceived {
		if m.Stage != HTTPSRequest || m.Request != HTTPSRequestAccepted || m.StatusCode < 200 || m.StatusCode > 599 {
			return invalid()
		}
		if m.ResponseTime != nil && (*m.ResponseTime < 0 || *m.ResponseTime > m.CompletedAt.Sub(m.StartedAt)) {
			return invalid()
		}
		return nil
	}
	if m.StatusCode != 0 || m.ResponseTime != nil {
		return invalid()
	}
	switch m.Exchange {
	case HTTPSConnectError:
		if m.Stage != HTTPSConnect {
			return invalid()
		}
	case HTTPSTLSError:
		if m.Stage != HTTPSTLS {
			return invalid()
		}
	case HTTPSTransportError:
		if m.Stage == HTTPSConnect {
			return invalid()
		}
	case HTTPSProtocolError:
		if m.Stage != HTTPSRequest || m.Request != HTTPSRequestAccepted {
			return invalid()
		}
	case HTTPSTimeout:
		if !m.CompletedAt.After(m.StartedAt) {
			return invalid()
		}
	case HTTPSIncomplete:
	default:
		return invalid()
	}
	return nil
}
