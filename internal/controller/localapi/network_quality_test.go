package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type localQualityTestHandler struct {
	networkTestHandler
	calls    atomic.Int32
	deadline atomic.Bool
	err      error
}

func (h *localQualityTestHandler) LocalNetworkQuality(ctx context.Context) (api.LocalNetworkQuality, error) {
	h.calls.Add(1)
	_, bounded := ctx.Deadline()
	h.deadline.Store(bounded)
	return api.LocalNetworkQuality{AsOf: time.Unix(100, 0).UTC(), Limitations: []string{"No probes."}}, h.err
}

func TestLocalQualityUDSRoundTripAndReadBoundary(t *testing.T) {
	handler := &localQualityTestHandler{}
	server := startMutationTestServer(t, handler)
	client := NewClient(server.stateDir)
	raw, err := client.Call(context.Background(), api.MethodNetworkQualityLocal)
	if err != nil {
		t.Fatal(err)
	}
	var result api.LocalNetworkQuality
	if err := json.Unmarshal(raw, &result); err != nil || result.Enrolled || result.Check != nil || result.AsOf.IsZero() {
		t.Fatalf("unexpected read: %+v, %v", result, err)
	}
	if handler.calls.Load() != 1 || !handler.deadline.Load() {
		t.Fatal("read did not use the bounded typed handler")
	}
	for _, params := range []any{
		map[string]string{"scope_id": "other"}, map[string]string{"interface_name": "lo0"},
		map[string]string{"url": "https://example.test"}, map[string]bool{"enable": true}, []string{"en0"},
	} {
		_, err := client.CallWithParams(context.Background(), api.MethodNetworkQualityLocal, params)
		if err == nil || !strings.Contains(err.Error(), "invalid_request") || handler.calls.Load() != 1 {
			t.Fatalf("caller input reached quality handler: %v", err)
		}
	}
}

func TestLocalQualityUDSAuthenticatesBeforeHandler(t *testing.T) {
	handler := &localQualityTestHandler{}
	server := startMutationTestServer(t, handler)
	for _, auth := range []string{"", strings.Repeat("f", 64)} {
		conn, err := net.Dial("unix", server.SocketPath())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		err = json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: "quality", Method: api.MethodNetworkQualityLocal, Auth: auth})
		if err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		var response api.Response
		err = json.NewDecoder(conn).Decode(&response)
		_ = conn.Close()
		if err != nil || response.Error == nil || response.Error.Code != "unauthorized" || handler.calls.Load() != 0 {
			t.Fatalf("unauthenticated request reached handler: %+v, %v", response, err)
		}
	}
}

func TestLocalQualityUDSFailsWithBoundedErrors(t *testing.T) {
	server := startMutationTestServer(t, &localQualityTestHandler{err: errors.New("secret-path and private diagnostics")})
	_, err := NewClient(server.stateDir).Call(context.Background(), api.MethodNetworkQualityLocal)
	if err == nil || !strings.Contains(err.Error(), "internal_error") || strings.Contains(err.Error(), "secret-path") {
		t.Fatalf("unsafe handler error: %v", err)
	}
	unsupported := startMutationTestServer(t, &networkTestHandler{})
	_, err = NewClient(unsupported.stateDir).Call(context.Background(), api.MethodNetworkQualityLocal)
	if err == nil || !strings.Contains(err.Error(), "method_not_found") {
		t.Fatalf("missing handler incorrectly accepted: %v", err)
	}
}
