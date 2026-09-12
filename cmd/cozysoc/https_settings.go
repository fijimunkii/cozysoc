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
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type httpsSettingsStore interface {
	CreateHTTPSConfiguration(context.Context, string, httpsplan.Configuration) (storage.HTTPSConfiguration, error)
	ListActiveHTTPSConfigurations(context.Context, string) ([]storage.HTTPSConfiguration, error)
	RetireHTTPSConfiguration(context.Context, string) error
}

func httpsConfiguration(p api.HTTPSSettingsParams) (httpsplan.Configuration, error) {
	endpoint, err := netip.ParseAddrPort(p.Endpoint)
	if err != nil || endpoint.String() != p.Endpoint {
		return httpsplan.Configuration{}, storage.ErrHTTPSConfiguration
	}
	c := httpsplan.Configuration{Endpoint: endpoint, ServerName: p.ServerName, RequestTarget: p.RequestTarget, DestinationPolicy: p.DestinationPolicy,
		Selection: nq.HTTPSSelection{ID: "validation", EndpointID: "validation", RequestID: "validation", Family: nq.AddressFamily(p.Family), Method: p.Method, ExpectedStatus: p.ExpectedStatus}}
	if httpsplan.ValidateConfiguration(c) != nil {
		return httpsplan.Configuration{}, storage.ErrHTTPSConfiguration
	}
	c.Selection.ID, c.Selection.EndpointID, c.Selection.RequestID = "", "", ""
	return c, nil
}

func projectHTTPSSettings(s storage.HTTPSConfiguration) api.HTTPSSettings {
	c := s.Disclosure()
	return api.HTTPSSettings{SelectionID: c.Selection.ID, EndpointID: c.Selection.EndpointID, RequestID: c.Selection.RequestID, ScopeID: s.ScopeID, CreatedAt: s.CreatedAt,
		Profile: httpsplan.Profile, Settings: api.HTTPSSettingsParams{Endpoint: c.Endpoint.String(), ServerName: c.ServerName, RequestTarget: c.RequestTarget, Family: string(c.Selection.Family), Method: c.Selection.Method, ExpectedStatus: c.Selection.ExpectedStatus, DestinationPolicy: c.DestinationPolicy}}
}

func (h *controllerAPIHandler) SaveHTTPS(ctx context.Context, params api.HTTPSSettingsParams) (api.HTTPSSettingsResult, error) {
	c, err := httpsConfiguration(params)
	if err != nil {
		return api.HTTPSSettingsResult{}, localapi.ErrInvalidRead
	}
	store, ok := h.store.(httpsSettingsStore)
	if !ok {
		return api.HTTPSSettingsResult{}, storage.ErrHTTPSConfiguration
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.HTTPSSettingsResult{}, storage.ErrHTTPSConfiguration
	}
	saved, err := store.CreateHTTPSConfiguration(ctx, binding.ScopeID, c)
	if err != nil {
		return api.HTTPSSettingsResult{}, err
	}
	return api.HTTPSSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.HTTPSSettings{projectHTTPSSettings(saved)}}, nil
}

func (h *controllerAPIHandler) ListHTTPSSettings(ctx context.Context) (api.HTTPSSettingsResult, error) {
	store, ok := h.store.(httpsSettingsStore)
	if !ok {
		return api.HTTPSSettingsResult{}, storage.ErrHTTPSConfiguration
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.HTTPSSettingsResult{}, storage.ErrHTTPSConfiguration
	}
	settings, err := store.ListActiveHTTPSConfigurations(ctx, binding.ScopeID)
	if err != nil {
		return api.HTTPSSettingsResult{}, err
	}
	result := api.HTTPSSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.HTTPSSettings{}}
	for _, item := range settings {
		result.Items = append(result.Items, projectHTTPSSettings(item))
	}
	return result, nil
}

func (h *controllerAPIHandler) RetireHTTPS(ctx context.Context, params api.HTTPSIDParams) (api.HTTPSRetireResult, error) {
	store, ok := h.store.(httpsSettingsStore)
	if !ok {
		return api.HTTPSRetireResult{}, storage.ErrHTTPSConfiguration
	}
	// Retirement must remain available after enrollment retirement or route loss.
	if err := store.RetireHTTPSConfiguration(ctx, params.SelectionID); err != nil {
		return api.HTTPSRetireResult{}, err
	}
	return api.HTTPSRetireResult{SchemaVersion: 1, SelectionID: params.SelectionID, State: "retired"}, nil
}

// Commands are explicit local configuration/disclosure. They never open a direct
// SQLite connection or install a second controller. JSON escapes control bytes.
func runHTTPSCommand(ctx context.Context, command string, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	var p api.HTTPSSettingsParams
	if command == "https-save" {
		fs.StringVar(&p.Endpoint, "endpoint", "", "explicit numeric IP:443 endpoint")
		fs.StringVar(&p.ServerName, "server-name", "", "explicit TLS identity and HTTP Host")
		fs.StringVar(&p.RequestTarget, "request-target", "", "exact origin-form path and optional query")
		fs.StringVar(&p.Family, "family", "", "transport family: ipv4 or ipv6")
		fs.StringVar(&p.Method, "method", "", "request method: GET or HEAD")
		fs.IntVar(&p.ExpectedStatus, "expected-status", 0, "explicit expected final HTTP status")
		fs.StringVar(&p.DestinationPolicy, "destination-policy", "", "policy: exact-endpoint")
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
	case "https-save":
		if fs.NArg() != 0 {
			return errors.New("https-save accepts only explicit settings flags")
		}
		if _, err := httpsConfiguration(p); err != nil {
			return errors.New("https-save requires valid explicit endpoint, server name, request target, family, method, expected status and destination policy")
		}
		method, params = api.MethodHTTPSSave, p
	case "https-list":
		if fs.NArg() != 0 {
			return errors.New("https-list accepts no positional arguments")
		}
		method, params = api.MethodHTTPSList, struct{}{}
	case "https-retire", "https-plan":
		if fs.NArg() != 1 || !localapi.ValidHTTPSSelectionID(fs.Arg(0)) {
			return errors.New("https command requires one opaque SELECTION_ID")
		}
		params = api.HTTPSIDParams{SelectionID: fs.Arg(0)}
		method = api.MethodHTTPSRetire
		if command == "https-plan" {
			method = api.MethodHTTPSPlan
		}
	default:
		return errors.New("unknown https command")
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
		return errors.New("https response is invalid")
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		return fmt.Errorf("write https response: %w", err)
	}
	return nil
}
