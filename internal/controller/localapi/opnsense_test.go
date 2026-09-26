package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type opnsenseTestHandler struct {
	testHandler
	calls  int
	params api.OPNsenseConnectParams
	err    error
}

func (h *opnsenseTestHandler) ConnectOPNsense(_ context.Context, params api.OPNsenseConnectParams) (api.OPNsenseConnection, error) {
	h.calls++
	h.params = params
	return api.OPNsenseConnection{Connected: true, Endpoint: params.Endpoint, Version: "26.7.4"}, h.err
}
func (h *opnsenseTestHandler) OPNsenseStatus(context.Context) (api.OPNsenseConnection, error) {
	h.calls++
	return api.OPNsenseConnection{Connected: true, Version: "26.7.4"}, h.err
}
func (h *opnsenseTestHandler) DisconnectOPNsense(context.Context) (api.OPNsenseConnection, error) {
	h.calls++
	return api.OPNsenseConnection{Connected: false}, h.err
}
func (h *opnsenseTestHandler) CollectOPNsense(_ context.Context, params api.OPNsenseCollectParams) (api.OPNsenseCollection, error) {
	h.calls++
	return api.OPNsenseCollection{ScopeID: params.ScopeID, Read: 2, Inserted: 1}, h.err
}

func TestOPNsenseNativeRoundTripAndSecretBoundary(t *testing.T) {
	h := &opnsenseTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	result, err := client.ConnectOPNsense(context.Background(), api.OPNsenseConnectParams{Endpoint: "https://192.168.1.1", APIKey: "private-key", APISecret: "private-secret", TrustPEM: "private-trust"})
	if err != nil || !result.Connected || h.params.APISecret != "private-secret" || h.params.TrustPEM != "private-trust" {
		t.Fatalf("connection round trip: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	for _, private := range []string{"private-key", "private-secret", "private-trust", "credential_ref"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private value leaked in result: %s", encoded)
		}
	}
	for _, params := range []any{map[string]string{"endpoint": "https://192.168.1.1", "api_key": "key"}, map[string]any{"endpoint": "https://192.168.1.1", "api_key": "key", "api_secret": "secret", "unknown": true}, []string{"https://192.168.1.1"}} {
		_, err := client.CallWithParams(context.Background(), api.MethodOPNsenseConnect, params)
		if err == nil || !strings.Contains(err.Error(), "invalid_request") || h.calls != 1 {
			t.Fatalf("invalid connection reached handler: %v, calls %d", err, h.calls)
		}
	}
	if _, err := client.OPNsenseStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := client.DisconnectOPNsense(context.Background()); err != nil || result.Connected || h.calls != 3 {
		t.Fatalf("disconnect %+v, %v, calls %d", result, err, h.calls)
	}
}

func TestOPNsenseNativeRequiresVerifiedPeerAndRedactsErrors(t *testing.T) {
	h := &opnsenseTestHandler{}
	s, err := newServer(t.TempDir(), h, slog.New(slog.NewTextHandler(io.Discard, nil)), func(net.Conn) (PeerIdentity, error) { return PeerIdentity{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx) }()
	t.Cleanup(func() { cancel(); _ = s.Close() })
	_, err = NewClient(s.stateDir).OPNsenseStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unauthorized") || h.calls != 0 {
		t.Fatalf("unverified peer reached handler: %v", err)
	}
	h.err = errors.New("private-router-credential and private path")
	verified := startMutationTestServer(t, h)
	_, err = NewClient(verified.stateDir).OPNsenseStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "internal_error") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe native error: %v", err)
	}
}

func TestOPNsenseCollectionReview(t *testing.T) {
	h := &opnsenseTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	params := api.OPNsenseCollectParams{ScopeID: "scope.home", Expected: api.OPNsenseCollectExpected{
		Endpoint: "https://192.168.1.1", Interface: api.NetworkInterface{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}},
	}}
	result, err := client.CollectOPNsense(context.Background(), params)
	if err != nil || result.Read != 2 || result.Inserted != 1 || h.calls != 1 {
		t.Fatalf("collection %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "192.168.1.10") || strings.Contains(string(encoded), "02:00:00") {
		t.Fatalf("private neighbor appeared in result: %s", encoded)
	}
	for _, invalid := range []any{map[string]any{"scope_id": "scope.home"}, map[string]any{"scope_id": "scope.home", "expected": map[string]any{"endpoint": "https://192.168.1.1", "interface": map[string]any{"interface_name": "en0", "unknown": true}}}, []string{"scope.home"}} {
		_, err := client.CallWithParams(context.Background(), api.MethodOPNsenseCollect, invalid)
		if err == nil || !strings.Contains(err.Error(), "invalid_request") || h.calls != 1 {
			t.Fatalf("invalid review reached handler: %v, calls %d", err, h.calls)
		}
	}
}
