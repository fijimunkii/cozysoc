package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type resolverSettingsStore interface {
	resolverConfigurationReader
	CreateResolverConfiguration(context.Context, string, resolverplan.Configuration) (storage.ResolverConfiguration, error)
	ListActiveResolverConfigurations(context.Context, string) ([]storage.ResolverConfiguration, error)
	RetireResolverConfiguration(context.Context, string) error
}

func resolverConfiguration(p api.ResolverSettingsParams) (resolverplan.Configuration, error) {
	endpoint, err := netip.ParseAddrPort(p.Endpoint)
	if err != nil || endpoint.String() != p.Endpoint {
		return resolverplan.Configuration{}, storage.ErrResolverConfiguration
	}
	c := resolverplan.Configuration{Endpoint: endpoint, Name: p.Name, DestinationScope: resolverplan.DestinationScope(p.DestinationScope),
		Selection: nq.ResolverSelection{ID: "validation", ResolverID: "validation", QueryID: "validation", Family: nq.AddressFamily(p.Family), Transport: nq.DNSTransport(p.Transport), QueryType: nq.DNSQueryType(p.QueryType), Expect: nq.DNSExpectation(p.Expect)}}
	if resolverplan.ValidateConfiguration(c) != nil {
		return resolverplan.Configuration{}, storage.ErrResolverConfiguration
	}
	c.Selection.ID, c.Selection.ResolverID, c.Selection.QueryID = "", "", ""
	return c, nil
}

func projectResolverSettings(s storage.ResolverConfiguration) api.ResolverSettings {
	c := s.Disclosure()
	return api.ResolverSettings{SelectionID: c.Selection.ID, ResolverID: c.Selection.ResolverID, QueryID: c.Selection.QueryID, ScopeID: s.ScopeID, CreatedAt: s.CreatedAt,
		Settings: api.ResolverSettingsParams{Endpoint: c.Endpoint.String(), Name: c.Name, Family: string(c.Selection.Family), Transport: string(c.Selection.Transport), QueryType: string(c.Selection.QueryType), Expect: string(c.Selection.Expect), DestinationScope: string(c.DestinationScope)}}
}

func (h *controllerAPIHandler) SaveResolver(ctx context.Context, params api.ResolverSettingsParams) (api.ResolverSettingsResult, error) {
	c, err := resolverConfiguration(params)
	if err != nil {
		return api.ResolverSettingsResult{}, localapi.ErrInvalidRead
	}
	store, ok := h.store.(resolverSettingsStore)
	if !ok {
		return api.ResolverSettingsResult{}, storage.ErrResolverConfiguration
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.ResolverSettingsResult{}, storage.ErrResolverConfiguration
	}
	saved, err := store.CreateResolverConfiguration(ctx, binding.ScopeID, c)
	if err != nil {
		return api.ResolverSettingsResult{}, err
	}
	return api.ResolverSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.ResolverSettings{projectResolverSettings(saved)}}, nil
}

func (h *controllerAPIHandler) ListResolvers(ctx context.Context) (api.ResolverSettingsResult, error) {
	store, ok := h.store.(resolverSettingsStore)
	if !ok {
		return api.ResolverSettingsResult{}, storage.ErrResolverConfiguration
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.ResolverSettingsResult{}, storage.ErrResolverConfiguration
	}
	settings, err := store.ListActiveResolverConfigurations(ctx, binding.ScopeID)
	if err != nil {
		return api.ResolverSettingsResult{}, err
	}
	result := api.ResolverSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.ResolverSettings{}}
	for _, item := range settings {
		result.Items = append(result.Items, projectResolverSettings(item))
	}
	return result, nil
}

func (h *controllerAPIHandler) RetireResolver(ctx context.Context, params api.ResolverIDParams) (api.ResolverRetireResult, error) {
	store, ok := h.store.(resolverSettingsStore)
	if !ok {
		return api.ResolverRetireResult{}, storage.ErrResolverConfiguration
	}
	// Retirement must remain available after enrollment retirement or route loss.
	if err := store.RetireResolverConfiguration(ctx, params.SelectionID); err != nil {
		return api.ResolverRetireResult{}, err
	}
	return api.ResolverRetireResult{SchemaVersion: 1, SelectionID: params.SelectionID, State: "retired"}, nil
}

func (h *controllerAPIHandler) PreviewResolver(ctx context.Context, params api.ResolverIDParams) (api.ResolverPlan, error) {
	selection, settings, err := h.collectResolverPlan(ctx, params.SelectionID)
	if err != nil {
		return api.ResolverPlan{}, err
	}
	d := selection.Plan.Disclosure()
	b := d.Budget
	return api.ResolverPlan{SchemaVersion: 1, Mode: "preview-only", Profile: d.Profile, Configuration: projectResolverSettings(settings),
		Binding:  api.GatewayPlanBinding{ScopeID: d.Binding.Observer.ScopeID, InterfaceName: d.Binding.Observer.InterfaceName, InterfaceIndex: d.Binding.Observer.InterfaceIndex, Prefixes: d.Binding.Prefixes},
		SensorID: d.Binding.Observer.SensorID, Source: d.Binding.Source.String(), CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt,
		OutsideEnrolledPrefixes: d.OutsideEnrolledPrefixes, MayForwardUpstream: d.MayForwardUpstream,
		Budget: api.ResolverPlanBudget{MaxSendCalls: b.MaxSendCalls, MaxRequestBytes: b.MaxRequestBytes, MaxReplyBytes: b.MaxReplyBytes, MaxReceivedDatagrams: b.MaxReceivedDatagrams, MaxReceiveCalls: b.MaxReceiveCalls, ExchangeTimeoutMS: b.ExchangeTimeout.Milliseconds(), TotalTimeoutMS: b.TotalTimeout.Milliseconds(), MaxConcurrentRuns: b.MaxConcurrentRuns, MinRunIntervalMS: b.MinRunInterval.Milliseconds()},
		Limitations: []string{
			"Preview only: no DNS traffic, consent, run ticket or execution authority is created.",
			"The selected resolver may forward this exact query upstream, including when its address is inside the enrolled prefixes.",
			"DNS byte ceilings exclude IP/link overhead, neighbor discovery and traffic the resolver generates upstream.",
			"A failed DNS query or timeout does not prove an internet outage or a security finding.",
			"Route metadata does not prove future socket binding. Actual execution requires fresh revalidation and explicit one-shot consent.",
			"Interface/prefix matching cannot distinguish networks reusing the same binding. Native route support does not certify physical NICs, VPNs, packaged permissions or sleep/resume.",
		}}, nil
}

// Commands are explicit local configuration/disclosure. They never open a direct
// SQLite connection or install a second controller. JSON escapes control bytes.
func runResolverCommand(ctx context.Context, command string, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	var p api.ResolverSettingsParams
	if command == "resolver-save" {
		fs.StringVar(&p.Endpoint, "endpoint", "", "explicit numeric IP:53 endpoint")
		fs.StringVar(&p.Name, "name", "", "explicit fully-qualified query name (trailing dot)")
		fs.StringVar(&p.Family, "family", "", "resolver transport family: ipv4 or ipv6")
		fs.StringVar(&p.Transport, "transport", "", "transport: udp")
		fs.StringVar(&p.QueryType, "query-type", "", "question type: A or AAAA")
		fs.StringVar(&p.Expect, "expect", "", "expected result: answer, nxdomain, or no-data")
		fs.StringVar(&p.DestinationScope, "destination-scope", "", "policy: enrolled-prefix or exact-endpoint")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	var method string
	var params any
	switch command {
	case "resolver-save":
		if fs.NArg() != 0 {
			return errors.New("resolver-save accepts only explicit settings flags")
		}
		if _, err := resolverConfiguration(p); err != nil {
			return errors.New("resolver-save requires valid explicit endpoint, name, family, transport, query type, expectation and destination scope")
		}
		method, params = api.MethodResolverSave, p
	case "resolver-list":
		if fs.NArg() != 0 {
			return errors.New("resolver-list accepts no positional arguments")
		}
		method, params = api.MethodResolverList, struct{}{}
	case "resolver-retire", "resolver-plan":
		if fs.NArg() != 1 || !localapi.ValidResolverSelectionID(fs.Arg(0)) {
			return errors.New("resolver command requires one opaque SELECTION_ID")
		}
		params = api.ResolverIDParams{SelectionID: fs.Arg(0)}
		method = api.MethodResolverRetire
		if command == "resolver-plan" {
			method = api.MethodResolverPlan
		}
	default:
		return errors.New("unknown resolver command")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	raw, err := localapi.NewClient(dir).CallWithParams(ctx, method, params)
	if err != nil {
		return err
	}
	// Re-encode as JSON, never write a raw response into a terminal.
	var output any
	if json.Unmarshal(raw, &output) != nil {
		return errors.New("resolver response is invalid")
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		return fmt.Errorf("write resolver response: %w", err)
	}
	return nil
}
