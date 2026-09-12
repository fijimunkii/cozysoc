package localapi

import (
	"net/netip"
	"reflect"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

func wireResolverBudget(b resolverplan.Budget) api.ResolverPlanBudget {
	return api.ResolverPlanBudget{MaxSendCalls: b.MaxSendCalls, MaxRequestBytes: b.MaxRequestBytes, MaxReplyBytes: b.MaxReplyBytes, MaxReceivedDatagrams: b.MaxReceivedDatagrams, MaxReceiveCalls: b.MaxReceiveCalls, ExchangeTimeoutMS: b.ExchangeTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(), MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()}
}
func cloneResolverReview(r api.ResolverCheckReview) api.ResolverCheckReview {
	r.Binding.Prefixes = slices.Clone(r.Binding.Prefixes)
	return r
}
func projectResolverReview(r resolverrun.Review, challenge string) api.ResolverCheckReview {
	d := r.Selection.Plan.Disclosure()
	c := d.Configuration
	b := d.Binding.Observer
	return api.ResolverCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Challenge: challenge, Profile: d.Profile, SelectionID: c.Selection.ID, ResolverID: c.Selection.ResolverID, QueryID: c.Selection.QueryID,
		Settings: api.ResolverSettingsParams{Endpoint: c.Endpoint.String(), Name: c.Name, Family: string(c.Selection.Family), Transport: string(c.Selection.Transport), QueryType: string(c.Selection.QueryType), Expect: string(c.Selection.Expect), DestinationScope: string(c.DestinationScope)},
		Binding:  api.GatewayPlanBinding{ScopeID: b.ScopeID, InterfaceName: b.InterfaceName, InterfaceIndex: b.InterfaceIndex, Prefixes: slices.Clone(d.Binding.Prefixes)}, SensorID: b.SensorID, Source: d.Binding.Source.String(), CreatedAt: d.CreatedAt, ExpiresAt: r.ExpiresAt.Round(0).UTC(), OutsideEnrolledPrefixes: d.OutsideEnrolledPrefixes, MayForwardUpstream: d.MayForwardUpstream, Budget: wireResolverBudget(d.Budget)}
}
func resolverWireSelection(r api.ResolverCheckReview) nq.ResolverSelection {
	p := r.Settings
	return nq.ResolverSelection{ID: r.SelectionID, ResolverID: r.ResolverID, QueryID: r.QueryID, Family: nq.AddressFamily(p.Family), Transport: nq.DNSTransport(p.Transport), QueryType: nq.DNSQueryType(p.QueryType), Expect: nq.DNSExpectation(p.Expect)}
}
func resolverWireObserver(r api.ResolverCheckReview) nq.Observer {
	return nq.Observer{ScopeID: r.Binding.ScopeID, SensorID: r.SensorID, InterfaceName: r.Binding.InterfaceName, InterfaceIndex: r.Binding.InterfaceIndex}
}

// Pure structural validation does not decode a ticket or grant authority. Only
// the process-local coordinator can authorize the original server-held plan.
func validateResolverReview(r api.ResolverCheckReview, id string, now time.Time) error {
	if r.SchemaVersion != 1 || r.Mode != "experimental-one-shot" || r.Profile != resolverrun.Profile || r.SelectionID != id || !ValidResolverSelectionID(id) || !gatewayChallenge.MatchString(r.Challenge) ||
		!gatewayTime(r.CreatedAt) || !gatewayTime(r.ExpiresAt) || r.CreatedAt.After(now) || !now.Before(r.ExpiresAt) || !r.ExpiresAt.After(r.CreatedAt) || r.ExpiresAt.Sub(r.CreatedAt) > resolverplan.ReviewLifetime {
		return errResolverProtocol
	}
	endpoint, err := netip.ParseAddrPort(r.Settings.Endpoint)
	if err != nil || endpoint.String() != r.Settings.Endpoint {
		return errResolverProtocol
	}
	source, err := netip.ParseAddr(r.Source)
	if err != nil || source.String() != r.Source {
		return errResolverProtocol
	}
	p, err := resolverplan.New(resolverplan.Binding{Observer: resolverWireObserver(r), Prefixes: r.Binding.Prefixes, Source: source}, resolverplan.Configuration{Selection: resolverWireSelection(r), Endpoint: endpoint, Name: r.Settings.Name, DestinationScope: resolverplan.DestinationScope(r.Settings.DestinationScope)}, r.CreatedAt)
	if err != nil {
		return errResolverProtocol
	}
	d := p.Disclosure()
	if wireResolverBudget(d.Budget) != r.Budget || d.Configuration.Name != r.Settings.Name || !slices.Equal(d.Binding.Prefixes, r.Binding.Prefixes) || d.OutsideEnrolledPrefixes != r.OutsideEnrolledPrefixes || !r.MayForwardUpstream {
		return errResolverProtocol
	}
	return nil
}
func projectResolverResult(review api.ResolverCheckReview, result resolverrun.Result, err error) api.ResolverCheckResult {
	out := api.ResolverCheckResult{SchemaVersion: 1, Review: cloneResolverReview(review), RunID: result.RunID, Outcome: result.Outcome}
	if err != nil {
		out.FailureCode = resolverErrorCode(err)
	}
	if s := result.Sample; s != nil {
		m := &api.ResolverRunMeasurement{StartedAt: s.StartedAt.Round(0).UTC(), CompletedAt: s.CompletedAt.Round(0).UTC(), Exchange: s.Exchange, Request: s.Request, Gap: s.Gap}
		if s.Reply != nil {
			reply := *s.Reply
			m.Reply = &reply
		}
		if s.ResponseTime != nil {
			ns := int64(*s.ResponseTime)
			m.ResponseTimeNanoseconds = &ns
		}
		out.Measurement = m
	}
	return out
}
func validateResolverResult(r api.ResolverCheckResult, review api.ResolverCheckReview, now time.Time) error {
	if r.SchemaVersion != 1 || !reflect.DeepEqual(r.Review, review) {
		return errResolverProtocol
	}
	if r.Outcome == "declined" {
		if r.RunID == "" && r.Measurement == nil && r.FailureCode == "" {
			return nil
		}
		return errResolverProtocol
	}
	if !gatewayChallenge.MatchString(r.RunID) {
		return errResolverProtocol
	}
	reason := ""
	switch r.Outcome {
	case "completed":
		if r.FailureCode != "" {
			return errResolverProtocol
		}
	case "failed":
		if r.FailureCode != "execution_failed" {
			return errResolverProtocol
		}
		reason = "execution-error"
	case "canceled":
		if r.FailureCode != "canceled" {
			return errResolverProtocol
		}
		reason = "canceled"
	case "indeterminate":
		if r.FailureCode != "execution_failed" || r.Measurement != nil {
			return errResolverProtocol
		}
		reason = "execution-panic"
	case "blocked":
		if r.Measurement != nil {
			return errResolverProtocol
		}
		switch r.FailureCode {
		case "precondition_failed":
			reason = "preflight-unavailable"
		case "review_expired":
			reason = "review-expired"
		default:
			return errResolverProtocol
		}
	default:
		return errResolverProtocol
	}
	if m := r.Measurement; m != nil {
		if m.StartedAt.Before(review.CreatedAt) || !m.StartedAt.Before(review.ExpiresAt) ||
			((m.Exchange == nq.DNSResponseReceived || m.Exchange == nq.DNSTimeout) && !m.CompletedAt.Before(review.ExpiresAt)) {
			return errResolverProtocol
		}
	}
	// Reuse the normalized event validator, including partial/uncertain evidence,
	// classic DNS reply semantics, completed timeout duration and RTT units.
	event := resolverrun.Event{SchemaVersion: 1, RunID: r.RunID, State: "finished", Outcome: r.Outcome, Reason: reason, At: now, Profile: resolverrun.Profile, Selection: resolverWireSelection(review), Observer: resolverWireObserver(review), Measurement: (*resolverrun.Measurement)(r.Measurement)}
	if resolverrun.ValidateEvent(event) != nil {
		return errResolverProtocol
	}
	return nil
}
