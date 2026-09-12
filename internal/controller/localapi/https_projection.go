package localapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var errHTTPSProtocol = errors.New("invalid HTTPS consent exchange")

func wireHTTPSBudget(b httpsplan.Budget) api.HTTPSPlanBudget {
	return api.HTTPSPlanBudget{MaxConnections: b.MaxConnections, MaxRequests: b.MaxRequests, MaxRetries: b.MaxRetries, MaxRequestBytes: b.MaxRequestBytes, MaxResponseHeaderBytes: b.MaxResponseHeaderBytes, MaxTransportReadBytes: b.MaxTransportReadBytes, MaxTransportWriteBytes: b.MaxTransportWriteBytes, MaxTransportReadCalls: b.MaxTransportReadCalls, MaxTransportWriteCalls: b.MaxTransportWriteCalls, ConnectTimeoutMS: b.ConnectTimeout.Milliseconds(), TLSHandshakeTimeoutMS: b.TLSHandshakeTimeout.Milliseconds(), ResponseHeaderTimeoutMS: b.ResponseHeaderTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(), MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()}
}

func wireHTTPSPolicy(b httpsplan.Policy) api.HTTPSPlanPolicy {
	return api.HTTPSPlanPolicy{ALPN: b.ALPN, TrustStore: b.TrustStore, ClientAuthentication: b.ClientAuthentication, HTTPVersion: b.HTTPVersion, MinTLSVersion: tls.VersionName(b.MinTLSVersion), MaxTLSVersion: tls.VersionName(b.MaxTLSVersion), VerifyServerIdentity: b.VerifyServerIdentity, FreshConnection: b.FreshConnection, SessionResumption: b.SessionResumption, EarlyData: b.EarlyData, UseProxy: b.UseProxy, ResolveNames: b.ResolveNames, FollowRedirects: b.FollowRedirects, ReadResponseBody: b.ReadResponseBody}
}

func httpsErrorCode(err error) string {
	switch {
	case errors.Is(err, httpsrun.ErrCooldown):
		return "cooldown"
	case errors.Is(err, httpsrun.ErrBusy):
		return "busy"
	case errors.Is(err, httpsrun.ErrReview):
		return "review_expired"
	case errors.Is(err, httpsrun.ErrPreflight):
		return "precondition_failed"
	case errors.Is(err, httpsrun.ErrAudit):
		return "audit_unconfirmed"
	case errors.Is(err, httpsrun.ErrClock):
		return "clock_invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, httpsrun.ErrExecution):
		return "execution_failed"
	default:
		return "unavailable"
	}
}

func projectHTTPSResult(review api.HTTPSCheckReview, result httpsrun.Result, err error) api.HTTPSCheckResult {
	out := api.HTTPSCheckResult{SchemaVersion: 1, Review: cloneHTTPSReview(review), RunID: result.RunID, Outcome: result.Outcome}
	if err != nil {
		out.FailureCode = httpsErrorCode(err)
	}
	if s := result.Sample; s != nil {
		m := &api.HTTPSRunMeasurement{StartedAt: s.StartedAt.Round(0).UTC(), CompletedAt: s.CompletedAt.Round(0).UTC(), Exchange: s.Exchange, Request: s.Request, Gap: s.Gap, Stage: s.Stage, StatusCode: s.StatusCode}
		if s.ResponseTime != nil {
			ns := int64(*s.ResponseTime)
			m.ResponseTimeNanoseconds = &ns
		}
		out.Measurement = m
	}
	return out
}
func validateHTTPSResult(r api.HTTPSCheckResult, review api.HTTPSCheckReview, now time.Time) error {
	if r.SchemaVersion != 1 || !reflect.DeepEqual(r.Review, review) {
		return errHTTPSProtocol
	}
	if r.Outcome == "declined" {
		if r.RunID == "" && r.Measurement == nil && r.FailureCode == "" {
			return nil
		}
		return errHTTPSProtocol
	}
	if !gatewayChallenge.MatchString(r.RunID) {
		return errHTTPSProtocol
	}
	reason := ""
	switch r.Outcome {
	case "completed":
		if r.FailureCode != "" {
			return errHTTPSProtocol
		}
	case "failed":
		if r.FailureCode != "execution_failed" {
			return errHTTPSProtocol
		}
		reason = "execution-error"
	case "canceled":
		if r.FailureCode != "canceled" {
			return errHTTPSProtocol
		}
		reason = "canceled"
	case "indeterminate":
		if r.FailureCode != "execution_failed" || r.Measurement != nil {
			return errHTTPSProtocol
		}
		reason = "execution-panic"
	case "blocked":
		if r.Measurement != nil {
			return errHTTPSProtocol
		}
		switch r.FailureCode {
		case "precondition_failed":
			reason = "preflight-unavailable"
		case "review_expired":
			reason = "review-expired"
		default:
			return errHTTPSProtocol
		}
	default:
		return errHTTPSProtocol
	}
	if m := r.Measurement; m != nil {
		if m.StartedAt.Before(review.CreatedAt) || !m.StartedAt.Before(review.ExpiresAt) ||
			((m.Exchange == nq.HTTPSResponseReceived || m.Exchange == nq.HTTPSTimeout) && !m.CompletedAt.Before(review.ExpiresAt)) {
			return errHTTPSProtocol
		}
	}
	// Reuse the normalized event validator, including partial/uncertain evidence,
	// HTTPS stage/request/status semantics and explicit timing units.
	event := httpsrun.Event{SchemaVersion: 1, RunID: r.RunID, State: "finished", Outcome: r.Outcome, Reason: reason, At: now, Profile: httpsrun.Profile, Selection: httpsWireSelection(review), Observer: httpsWireObserver(review), Measurement: (*httpsrun.Measurement)(r.Measurement)}
	if httpsrun.ValidateEvent(event) != nil {
		return errHTTPSProtocol
	}
	return nil
}

func cloneHTTPSReview(r api.HTTPSCheckReview) api.HTTPSCheckReview {
	r.Binding.Prefixes = slices.Clone(r.Binding.Prefixes)
	r.Privacy = slices.Clone(r.Privacy)
	return r
}

func projectHTTPSReview(r httpsrun.Review, challenge string) api.HTTPSCheckReview {
	d := r.Selection.Plan.Disclosure()
	c := d.Configuration
	b := d.Binding.Observer
	request, _ := r.Selection.Plan.RequestBytes() // Invalid plans fail validation below.
	return api.HTTPSCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Challenge: challenge, Profile: d.Profile, SelectionID: c.Selection.ID, EndpointID: c.Selection.EndpointID, RequestID: c.Selection.RequestID,
		Settings: api.HTTPSSettingsParams{Endpoint: c.Endpoint.String(), ServerName: c.ServerName, RequestTarget: c.RequestTarget, Family: string(c.Selection.Family), Method: c.Selection.Method, ExpectedStatus: c.Selection.ExpectedStatus, DestinationPolicy: string(c.DestinationPolicy)},
		Binding:  api.GatewayPlanBinding{ScopeID: b.ScopeID, InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex, Prefixes: slices.Clone(d.Binding.Prefixes)}, SensorID: b.SensorID, Source: d.Binding.Source.String(), CreatedAt: d.CreatedAt, ExpiresAt: r.ExpiresAt.Round(0).UTC(), RouteObservedAt: r.Selection.RouteObservedAt.Round(0).UTC(), RouteFreshUntil: r.Selection.RouteFreshUntil.Round(0).UTC(), OutsideEnrolledPrefixes: d.OutsideEnrolledPrefixes, Budget: wireHTTPSBudget(d.Budget), Policy: wireHTTPSPolicy(d.Policy), RequestBytes: string(request), Privacy: slices.Clone(d.Privacy)}
}
func httpsWireSelection(r api.HTTPSCheckReview) nq.HTTPSSelection {
	p := r.Settings
	return nq.HTTPSSelection{ID: r.SelectionID, EndpointID: r.EndpointID, RequestID: r.RequestID, Family: nq.AddressFamily(p.Family), Method: p.Method, ExpectedStatus: p.ExpectedStatus}
}
func httpsWireObserver(r api.HTTPSCheckReview) nq.Observer {
	return nq.Observer{ScopeID: r.Binding.ScopeID, SensorID: r.SensorID, InterfaceName: r.Binding.InterfaceName, InterfaceIndex: r.Binding.InterfaceIndex}
}

// Validation reconstructs the complete fixed profile. A structurally valid review
// is still not consent: the server must retain the original ticket and selection.
func validateHTTPSReview(r api.HTTPSCheckReview, id string, now time.Time) error {
	if r.SchemaVersion != 1 || r.Mode != "experimental-one-shot" || r.Profile != httpsrun.Profile || r.SelectionID != id || !ValidHTTPSSelectionID(id) || !gatewayChallenge.MatchString(r.Challenge) ||
		!gatewayTime(now) || !gatewayTime(r.CreatedAt) || !gatewayTime(r.ExpiresAt) || !gatewayTime(r.RouteObservedAt) || !gatewayTime(r.RouteFreshUntil) || r.CreatedAt.After(now) || !now.Before(r.ExpiresAt) || !r.ExpiresAt.After(r.CreatedAt) || r.ExpiresAt.Sub(r.CreatedAt) > httpsplan.ReviewLifetime || r.RouteObservedAt.After(r.CreatedAt) || !r.RouteFreshUntil.Equal(r.RouteObservedAt.Add(httpsplan.ReviewLifetime)) || !now.Before(r.RouteFreshUntil) {
		return errHTTPSProtocol
	}
	endpoint, err := netip.ParseAddrPort(r.Settings.Endpoint)
	if err != nil || endpoint.String() != r.Settings.Endpoint {
		return errHTTPSProtocol
	}
	source, err := netip.ParseAddr(r.Source)
	if err != nil || source.String() != r.Source {
		return errHTTPSProtocol
	}
	p, err := httpsplan.New(httpsplan.Binding{Observer: httpsWireObserver(r), Prefixes: r.Binding.Prefixes, Source: source}, httpsplan.Configuration{Selection: httpsWireSelection(r), Endpoint: endpoint, ServerName: r.Settings.ServerName, RequestTarget: r.Settings.RequestTarget, DestinationPolicy: r.Settings.DestinationPolicy}, r.CreatedAt)
	if err != nil {
		return errHTTPSProtocol
	}
	d := p.Disclosure()
	expires := d.ExpiresAt
	if r.RouteFreshUntil.Before(expires) {
		expires = r.RouteFreshUntil
	}
	expected := projectHTTPSReview(httpsrun.Review{Selection: httpsrun.Selection{Plan: p, RouteObservedAt: r.RouteObservedAt, RouteFreshUntil: r.RouteFreshUntil}, ExpiresAt: expires}, r.Challenge)
	if !reflect.DeepEqual(expected, r) {
		return errHTTPSProtocol
	}
	return nil
}
