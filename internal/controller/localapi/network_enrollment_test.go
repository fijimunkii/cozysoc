package localapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type networkTestHandler struct {
	enrollErr error
}

func (*networkTestHandler) Status() api.Status {
	return api.Status{APIVersion: api.Version, ControllerVersion: "test", Transport: "unix"}
}

func (*networkTestHandler) Health() api.Health {
	return api.Health{State: "ok", LastTickAt: time.Unix(1, 0).UTC()}
}

func (*networkTestHandler) Capabilities() api.CapabilityList {
	return api.CapabilityList{CatalogSchemaVersion: 1}
}

func (*networkTestHandler) Networks(context.Context) (api.NetworkList, error) {
	return api.NetworkList{
		Candidates: []api.NetworkInterface{{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}},
	}, nil
}

func (h *networkTestHandler) EnrollNetwork(_ context.Context, params api.NetworkEnrollParams) (api.NetworkEnrollResult, error) {
	if h.enrollErr != nil {
		return api.NetworkEnrollResult{}, h.enrollErr
	}
	return api.NetworkEnrollResult{
		ScopeID:    "scope.one",
		EnrolledAt: time.Unix(2, 0).UTC(),
		Interface:  api.NetworkInterface{InterfaceName: params.InterfaceName, InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}},
		Changed:    true,
	}, nil
}

func TestNetworksListAndEnrollRoundTrip(t *testing.T) {
	server := startMutationTestServer(t, &networkTestHandler{})
	client := NewClient(server.stateDir)
	result, err := client.Call(context.Background(), api.MethodNetworksList)
	if err != nil {
		t.Fatal(err)
	}
	var list api.NetworkList
	if err := json.Unmarshal(result, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Candidates) != 1 || list.Candidates[0].InterfaceName != "en0" {
		t.Fatalf("unexpected network list: %+v", list)
	}

	result, err = client.CallWithParams(context.Background(), api.MethodNetworkEnroll, api.NetworkEnrollParams{InterfaceName: "en0"})
	if err != nil {
		t.Fatal(err)
	}
	var enrolled api.NetworkEnrollResult
	if err := json.Unmarshal(result, &enrolled); err != nil {
		t.Fatal(err)
	}
	if !enrolled.Changed || enrolled.ScopeID != "scope.one" || enrolled.Interface.InterfaceName != "en0" {
		t.Fatalf("unexpected enrollment result: %+v", enrolled)
	}
}

func TestNetworkEnrollRejectsMissingUnknownAndReadParams(t *testing.T) {
	server := startMutationTestServer(t, &networkTestHandler{})
	client := NewClient(server.stateDir)
	for name, call := range map[string]func() error{
		"missing": func() error {
			_, err := client.Call(context.Background(), api.MethodNetworkEnroll)
			return err
		},
		"unknown": func() error {
			_, err := client.CallWithParams(context.Background(), api.MethodNetworkEnroll, map[string]any{"interface_name": "en0", "extra": true})
			return err
		},
		"read params": func() error {
			_, err := client.CallWithParams(context.Background(), api.MethodNetworksList, map[string]bool{"unexpected": true})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "invalid_request") {
				t.Fatalf("request error = %v", err)
			}
		})
	}
}

func TestNetworkEnrollMapsSafeHandlerErrors(t *testing.T) {
	for name, test := range map[string]struct {
		handlerErr error
		want       string
	}{
		"invalid":      {handlerErr: ErrInvalidMutation, want: "invalid_request"},
		"precondition": {handlerErr: ErrMutationPrecondition, want: "precondition_failed"},
		"conflict":     {handlerErr: ErrMutationConflict, want: "conflict"},
		"internal":     {handlerErr: context.DeadlineExceeded, want: "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			server := startMutationTestServer(t, &networkTestHandler{enrollErr: test.handlerErr})
			_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodNetworkEnroll, api.NetworkEnrollParams{InterfaceName: "en0"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("handler error = %v", err)
			}
		})
	}
}
