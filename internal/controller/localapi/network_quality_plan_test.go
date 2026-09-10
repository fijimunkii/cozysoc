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

type gatewayPlanTestHandler struct {
	networkTestHandler
	calls   atomic.Int32
	target  atomic.Value
	bounded atomic.Bool
	err     error
}

func (h *gatewayPlanTestHandler) PreviewGatewayCheck(ctx context.Context, params api.GatewayPlanParams) (api.GatewayCheckPlan, error) {
	h.calls.Add(1)
	h.target.Store(params.Target)
	deadline, ok := ctx.Deadline()
	h.bounded.Store(ok && time.Until(deadline) <= requestTimeout)
	return api.GatewayCheckPlan{SchemaVersion: 1, Mode: "preview-only", Target: api.GatewayPlanTarget{Address: params.Target}}, h.err
}

func TestGatewayPlanUDSReadBoundary(t *testing.T) {
	h := &gatewayPlanTestHandler{}
	server := startMutationTestServer(t, h)
	client := NewClient(server.stateDir)
	raw, err := client.CallWithParams(context.Background(), api.MethodNetworkQualityGatewayPlan, api.GatewayPlanParams{Target: "192.168.50.1"})
	if err != nil {
		t.Fatal(err)
	}
	var plan api.GatewayCheckPlan
	if err := json.Unmarshal(raw, &plan); err != nil || plan.Mode != "preview-only" || plan.ExecutionAvailable || plan.ConsentGranted || h.target.Load() != "192.168.50.1" || !h.bounded.Load() {
		t.Fatalf("unexpected preview: %+v, %v", plan, err)
	}
	for _, params := range []any{nil, json.RawMessage(`null`), map[string]string{}, []string{"192.168.50.1"},
		map[string]any{"target": "192.168.50.1", "scope_id": "other"},
		map[string]any{"target": "192.168.50.1", "interface_name": "lo"},
		map[string]any{"target": "192.168.50.1", "max_attempts": 1000},
		map[string]any{"target": "192.168.50.1", "consent": true},
		map[string]any{"target": "192.168.50.1", "execute": true},
		map[string]any{"target": "192.168.50.1", "url": "https://example.test"},
		map[string]any{"target": 42}, map[string]any{"target": strings.Repeat("a", 16)},
	} {
		_, err := client.CallWithParams(context.Background(), api.MethodNetworkQualityGatewayPlan, params)
		if err == nil || !strings.Contains(err.Error(), "invalid_request") || h.calls.Load() != 1 {
			t.Fatalf("invalid params reached handler: %v", err)
		}
	}
	for _, method := range []string{"network-quality.gateway-run", "network-quality.gateway-approve"} {
		_, err := client.Call(context.Background(), method)
		if err == nil || !strings.Contains(err.Error(), "method_not_found") {
			t.Fatalf("invented executor/consent method: %v", err)
		}
	}
}

func TestGatewayPlanUDSAuthenticationPrecedesHandler(t *testing.T) {
	h := &gatewayPlanTestHandler{}
	server := startMutationTestServer(t, h)
	for _, auth := range []string{"", "wrong-session"} {
		conn, err := net.Dial("unix", server.SocketPath())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		err = json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: "plan", Method: api.MethodNetworkQualityGatewayPlan,
			Auth: auth, Params: json.RawMessage(`{"target":"192.168.50.1"}`)})
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		var response api.Response
		err = json.NewDecoder(conn).Decode(&response)
		conn.Close()
		if err != nil || response.Error == nil || response.Error.Code != "unauthorized" || h.calls.Load() != 0 {
			t.Fatalf("auth boundary failed: %+v, %v", response, err)
		}
	}
}

func TestGatewayPlanUDSErrorsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{ErrInvalidRead, "invalid_request"}, {ErrReadTargetNotFound, "not_found"}, {ErrGatewayPlanPrecondition, "precondition_failed"},
		{errors.New("private-database-secret"), "internal_error"}, {context.DeadlineExceeded, "internal_error"},
	} {
		server := startMutationTestServer(t, &gatewayPlanTestHandler{err: tc.err})
		_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodNetworkQualityGatewayPlan, api.GatewayPlanParams{Target: "192.168.50.1"})
		if err == nil || !strings.Contains(err.Error(), tc.code) || strings.Contains(err.Error(), "private-database") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	server := startMutationTestServer(t, &networkTestHandler{})
	_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodNetworkQualityGatewayPlan, api.GatewayPlanParams{Target: "192.168.50.1"})
	if err == nil || !strings.Contains(err.Error(), "method_not_found") {
		t.Fatalf("missing handler error: %v", err)
	}
}
