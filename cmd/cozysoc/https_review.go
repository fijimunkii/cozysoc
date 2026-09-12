package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/netip"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

var errHTTPSReview = errors.New("HTTPS review unavailable")

type httpsConfigurationReader interface {
	ActiveHTTPSConfiguration(context.Context, string) (storage.HTTPSConfiguration, error)
}

func (h *controllerAPIHandler) httpsInputs(ctx context.Context, id string) (storage.HTTPSConfiguration, httpsroute.Enrollment, error) {
	if h == nil || h.store == nil || ctx.Err() != nil {
		return storage.HTTPSConfiguration{}, httpsroute.Enrollment{}, errHTTPSReview
	}
	reader, ok := h.store.(httpsConfigurationReader)
	if !ok {
		return storage.HTTPSConfiguration{}, httpsroute.Enrollment{}, errHTTPSReview
	}
	settings, err := reader.ActiveHTTPSConfiguration(ctx, id)
	if err != nil {
		return storage.HTTPSConfiguration{}, httpsroute.Enrollment{}, errHTTPSReview
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil || binding.ScopeID != settings.ScopeID || ctx.Err() != nil {
		return storage.HTTPSConfiguration{}, httpsroute.Enrollment{}, errHTTPSReview
	}
	prefixes := make([]string, 0, len(binding.Prefixes))
	for _, raw := range binding.Prefixes {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return storage.HTTPSConfiguration{}, httpsroute.Enrollment{}, errHTTPSReview
		}
		// Passive enrollment includes link-local neighbor prefixes. The HTTPS route
		// profile compares the complete routable set and cannot send to link-local.
		if p.Addr().IsLinkLocalUnicast() {
			bits := 10
			if p.Addr().Is4() {
				bits = 16
			}
			if p.Bits() < bits {
				return storage.HTTPSConfiguration{}, httpsroute.Enrollment{}, errHTTPSReview
			}
			continue
		}
		prefixes = append(prefixes, raw)
	}
	slices.Sort(prefixes)
	return settings, httpsroute.Enrollment{Observer: nq.Observer{ScopeID: binding.ScopeID, SensorID: "controller-selected-https", InterfaceName: binding.InterfaceName, InterfaceIndex: binding.InterfaceIndex}, Prefixes: prefixes}, nil
}

// Each preflight re-reads immutable settings and durable enrollment around native
// route collection. Never reconstruct authority from a client preview or cache.
func (h *controllerAPIHandler) collectHTTPSPlan(ctx context.Context, id string) (httpsroute.Selection, storage.HTTPSConfiguration, error) {
	fail := func() (httpsroute.Selection, storage.HTTPSConfiguration, error) {
		return httpsroute.Selection{}, storage.HTTPSConfiguration{}, errHTTPSReview
	}
	if h == nil || h.now == nil || h.httpsRouteInspector == nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := h.now()
	settings, enrolled, err := h.httpsInputs(ctx, id)
	if err != nil {
		return fail()
	}
	selection, err := h.httpsRouteInspector.Inspect(ctx, enrolled, settings.Disclosure())
	if err != nil {
		return fail()
	}
	current, binding, err := h.httpsInputs(ctx, id)
	if err != nil || current.Disclosure() != settings.Disclosure() || current.ScopeID != settings.ScopeID || !current.CreatedAt.Equal(settings.CreatedAt) ||
		enrolled.Observer != binding.Observer || !slices.Equal(enrolled.Prefixes, binding.Prefixes) {
		return fail()
	}
	d := selection.Plan.Disclosure()
	expected, err := httpsplan.New(httpsplan.Binding{Observer: binding.Observer, Prefixes: binding.Prefixes, Source: d.Binding.Source}, current.Disclosure(), d.CreatedAt)
	now := h.now()
	if err != nil || !expected.SameSelection(selection.Plan) || !selection.Plan.Current(now) || now.Before(started) || now.Sub(started) >= 5*time.Second ||
		selection.RouteObservedAt.IsZero() || selection.RouteFreshUntil.IsZero() || selection.RouteObservedAt.Before(started) || selection.RouteObservedAt.After(d.CreatedAt) || selection.RouteFreshUntil.After(selection.RouteObservedAt.Add(httpsplan.ReviewLifetime)) ||
		!now.Before(selection.RouteFreshUntil) || ctx.Err() != nil {
		return fail()
	}
	return selection, current, nil
}

func (h *controllerAPIHandler) PreviewHTTPS(ctx context.Context, p api.HTTPSIDParams) (api.HTTPSPlan, error) {
	selection, settings, err := h.collectHTTPSPlan(ctx, p.SelectionID)
	if err != nil {
		return api.HTTPSPlan{}, err
	}
	d := selection.Plan.Disclosure()
	request, err := selection.Plan.RequestBytes()
	if err != nil {
		return api.HTTPSPlan{}, errHTTPSReview
	}
	return api.HTTPSPlan{SchemaVersion: 1, Mode: "preview-only", Profile: d.Profile, Configuration: projectHTTPSSettings(settings),
		Binding:  api.GatewayPlanBinding{ScopeID: d.Binding.Observer.ScopeID, InterfaceName: d.Binding.Observer.InterfaceName, InterfaceIndex: d.Binding.Observer.InterfaceIndex, Prefixes: d.Binding.Prefixes},
		SensorID: d.Binding.Observer.SensorID, Source: d.Binding.Source.String(), CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt, RouteObservedAt: selection.RouteObservedAt, RouteFreshUntil: selection.RouteFreshUntil,
		OutsideEnrolledPrefixes: d.OutsideEnrolledPrefixes, Policy: projectHTTPSPolicy(d.Policy), Budget: projectHTTPSBudget(d.Budget), RequestBytes: string(request), Privacy: d.Privacy,
		Limitations: []string{"Preview only: no HTTPS traffic, consent, run ticket or execution authority is created.", "Route metadata does not prove future TCP socket binding or endpoint reachability. Actual execution requires fresh revalidation and explicit one-shot consent.", "Interface/prefix matching cannot distinguish networks reusing the same binding. Native route support does not certify physical NICs, VPNs, packaged permissions or sleep/resume."}}, nil
}

func projectHTTPSBudget(b httpsplan.Budget) api.HTTPSPlanBudget {
	return api.HTTPSPlanBudget{MaxConnections: b.MaxConnections, MaxRequests: b.MaxRequests, MaxRetries: b.MaxRetries, MaxRequestBytes: b.MaxRequestBytes, MaxResponseHeaderBytes: b.MaxResponseHeaderBytes, MaxTransportReadBytes: b.MaxTransportReadBytes, MaxTransportWriteBytes: b.MaxTransportWriteBytes, MaxTransportReadCalls: b.MaxTransportReadCalls, MaxTransportWriteCalls: b.MaxTransportWriteCalls, ConnectTimeoutMS: b.ConnectTimeout.Milliseconds(), TLSHandshakeTimeoutMS: b.TLSHandshakeTimeout.Milliseconds(), ResponseHeaderTimeoutMS: b.ResponseHeaderTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(), MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()}
}

func projectHTTPSPolicy(b httpsplan.Policy) api.HTTPSPlanPolicy {
	return api.HTTPSPlanPolicy{ALPN: b.ALPN, TrustStore: b.TrustStore, ClientAuthentication: b.ClientAuthentication, HTTPVersion: b.HTTPVersion, MinTLSVersion: tls.VersionName(b.MinTLSVersion), MaxTLSVersion: tls.VersionName(b.MaxTLSVersion), VerifyServerIdentity: b.VerifyServerIdentity, FreshConnection: b.FreshConnection, SessionResumption: b.SessionResumption, EarlyData: b.EarlyData, UseProxy: b.UseProxy, ResolveNames: b.ResolveNames, FollowRedirects: b.FollowRedirects, ReadResponseBody: b.ReadResponseBody}
}
