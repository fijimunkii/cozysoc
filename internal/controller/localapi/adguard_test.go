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

type adguardTestHandler struct {
	testHandler
	calls  int
	params api.AdGuardConnectParams
	err    error
}

func (h *adguardTestHandler) CollectAdGuard(_ context.Context, params api.AdGuardCollectParams) (api.AdGuardCollection, error) {
	h.calls++
	return api.AdGuardCollection{ScopeID: params.ScopeID, Read: 2, Inserted: 1, SkippedOutsideScope: 1}, h.err
}

func (h *adguardTestHandler) ConnectAdGuard(_ context.Context, params api.AdGuardConnectParams) (api.AdGuardConnection, error) {
	h.calls++
	h.params = params
	return api.AdGuardConnection{Connected: true, Endpoint: params.Endpoint, Running: true}, h.err
}
func (h *adguardTestHandler) AdGuardStatus(context.Context) (api.AdGuardConnection, error) {
	h.calls++
	return api.AdGuardConnection{Connected: true, Running: true}, h.err
}
func (h *adguardTestHandler) DisconnectAdGuard(context.Context) (api.AdGuardConnection, error) {
	h.calls++
	return api.AdGuardConnection{Connected: false}, h.err
}

func TestAdGuardNativeRoundTripAndParameterBoundary(t *testing.T) {
	h := &adguardTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	result, err := client.ConnectAdGuard(context.Background(), api.AdGuardConnectParams{Endpoint: "http://127.0.0.1:3000", Username: "reader", Password: "private-password"})
	if err != nil || !result.Connected || !result.Running || h.params.Password != "private-password" {
		t.Fatalf("connect result %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private-password") || strings.Contains(string(encoded), "credential_ref") {
		t.Fatal("secret leaked in native result")
	}
	for _, params := range []any{map[string]string{"endpoint": "http://127.0.0.1", "password": "orphan"}, map[string]any{"endpoint": "http://127.0.0.1", "unknown": true}, []string{"http://127.0.0.1"}} {
		_, err := client.CallWithParams(context.Background(), api.MethodAdGuardConnect, params)
		if err == nil || !strings.Contains(err.Error(), "invalid_request") || h.calls != 1 {
			t.Fatalf("invalid connection reached handler: %v, calls %d", err, h.calls)
		}
	}
	if _, err := client.AdGuardStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := client.DisconnectAdGuard(context.Background()); err != nil || result.Connected || h.calls != 3 {
		t.Fatalf("disconnect %+v, %v, calls %d", result, err, h.calls)
	}
}

func TestAdGuardNativeRejectsUnverifiedPeerAndSanitizesErrors(t *testing.T) {
	h := &adguardTestHandler{}
	s, err := newServer(t.TempDir(), h, slog.New(slog.NewTextHandler(io.Discard, nil)), func(net.Conn) (PeerIdentity, error) { return PeerIdentity{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx) }()
	t.Cleanup(func() { cancel(); _ = s.Close() })
	_, err = NewClient(s.stateDir).AdGuardStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unauthorized") || h.calls != 0 {
		t.Fatalf("unverified peer reached handler: %v", err)
	}
	h.err = errors.New("private-password and private path")
	verified := startMutationTestServer(t, h)
	_, err = NewClient(verified.stateDir).AdGuardStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "internal_error") || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe native error: %v", err)
	}
}

func TestAdGuardCollectionNativeBoundaryReturnsCountsOnly(t *testing.T) {
	h := &adguardTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	result, err := client.CollectAdGuard(context.Background(), "scope.home")
	if err != nil || result.ScopeID != "scope.home" || result.Read != 2 || result.Inserted != 1 || h.calls != 1 {
		t.Fatalf("native collection %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private.example") || strings.Contains(string(encoded), `"client_ip":`) || strings.Contains(string(encoded), "192.0.2.4") {
		t.Fatalf("private query data leaked: %s", encoded)
	}
	for _, params := range []any{map[string]string{"scope_id": ""}, map[string]any{"scope_id": "scope.home", "unknown": true}, []string{"scope.home"}} {
		_, err := client.CallWithParams(context.Background(), api.MethodAdGuardCollect, params)
		if err == nil || !strings.Contains(err.Error(), "invalid_request") || h.calls != 1 {
			t.Fatalf("invalid collection reached handler: %v, calls %d", err, h.calls)
		}
	}
}
