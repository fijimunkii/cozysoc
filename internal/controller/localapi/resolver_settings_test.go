package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type resolverSettingsTestHandler struct {
	testHandler
	calls   atomic.Int32
	bounded atomic.Bool
	err     error
}

func (h *resolverSettingsTestHandler) hit(ctx context.Context) {
	h.calls.Add(1)
	deadline, ok := ctx.Deadline()
	h.bounded.Store(ok && time.Until(deadline) <= requestTimeout)
}
func (h *resolverSettingsTestHandler) SaveResolver(ctx context.Context, p api.ResolverSettingsParams) (api.ResolverSettingsResult, error) {
	h.hit(ctx)
	return api.ResolverSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.ResolverSettings{{Settings: p}}}, h.err
}
func (h *resolverSettingsTestHandler) ListResolvers(ctx context.Context) (api.ResolverSettingsResult, error) {
	h.hit(ctx)
	return api.ResolverSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.ResolverSettings{}}, h.err
}
func (h *resolverSettingsTestHandler) RetireResolver(ctx context.Context, p api.ResolverIDParams) (api.ResolverRetireResult, error) {
	h.hit(ctx)
	return api.ResolverRetireResult{SchemaVersion: 1, SelectionID: p.SelectionID, State: "retired"}, h.err
}
func (h *resolverSettingsTestHandler) PreviewResolver(ctx context.Context, p api.ResolverIDParams) (api.ResolverPlan, error) {
	h.hit(ctx)
	return api.ResolverPlan{SchemaVersion: 1, Mode: "preview-only"}, h.err
}

func TestResolverNativeMethodsAndExactParameters(t *testing.T) {
	h := &resolverSettingsTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	ctx := context.Background()
	id := "selection." + strings.Repeat("a", 32)
	p := api.ResolverSettingsParams{Endpoint: "192.0.2.53:53", Name: "private.example.", Family: "ipv4", Transport: "udp", QueryType: "A", Expect: "answer", DestinationScope: "enrolled-prefix"}
	for _, tc := range []struct {
		method string
		params any
	}{{api.MethodResolverSave, p}, {api.MethodResolverList, struct{}{}}, {api.MethodResolverRetire, api.ResolverIDParams{SelectionID: id}}, {api.MethodResolverPlan, api.ResolverIDParams{SelectionID: id}}} {
		if _, err := client.CallWithParams(ctx, tc.method, tc.params); err != nil {
			t.Fatal(err)
		}
	}
	if h.calls.Load() != 4 || !h.bounded.Load() {
		t.Fatal("typed methods not bounded")
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"selection_id":null}`, `{"selection_id":"192.0.2.53"}`, `{"selection_id":"` + id + `","approve":true}`, `{"selection_id":"` + id + `","selection_id":"` + id + `"}`, `{"Selection_ID":"` + id + `"}`, `{"selection_id":"` + id + `","endpoint":"192.0.2.53:53"}`} {
		if _, err := client.CallWithParams(ctx, api.MethodResolverPlan, json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "invalid_request") {
			t.Fatal("invalid parameters accepted", err)
		}
	}
	data, _ := json.Marshal(p)
	for _, field := range []string{`"scope_id":"other"`, `"consent":true`, `"source":"192.0.2.1"`, `"selection_id":"old"`, `"budget":1`, `"name":"second.example."`} {
		raw := string(data[:len(data)-1]) + "," + field + "}"
		if _, err := client.CallWithParams(ctx, api.MethodResolverSave, json.RawMessage(raw)); err == nil {
			t.Fatal("save accepted extra/duplicate field")
		}
	}
	if h.calls.Load() != 4 {
		t.Fatal("invalid parameters reached handler")
	}
	for _, method := range []string{"network-quality.resolver-run", "network-quality.resolver-approve"} {
		if _, err := client.Call(ctx, method); err == nil || !strings.Contains(err.Error(), "method_not_found") {
			t.Fatal("implicit execution endpoint", err)
		}
	}
}

func TestResolverAuthentication(t *testing.T) {
	h := &resolverSettingsTestHandler{}
	s := startMutationTestServer(t, h)
	for _, method := range []string{api.MethodResolverSave, api.MethodResolverList, api.MethodResolverRetire, api.MethodResolverPlan} {
		conn, err := net.Dial("unix", s.SocketPath())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if err := json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: "test", Method: method, Auth: "wrong", Params: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		var response api.Response
		err = json.NewDecoder(conn).Decode(&response)
		conn.Close()
		if err != nil || response.Error == nil || response.Error.Code != "unauthorized" || h.calls.Load() != 0 {
			t.Fatal("private method before authentication", err)
		}
	}
	// Platforms without kernel verification cannot expose settings based on the
	// bearer secret alone, even if a verifier returns no error.
	unverified, err := newServer(t.TempDir(), h, nil, func(net.Conn) (PeerIdentity, error) { return PeerIdentity{UID: os.Geteuid(), Verified: false}, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = unverified.Serve(ctx) }()
	defer func() { cancel(); _ = unverified.Close(); <-done }()

	if _, err := NewClient(unverified.stateDir).CallWithParams(context.Background(), api.MethodResolverList, struct{}{}); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatal("unverified identity accepted", err)
	}
}

func TestResolverNativeErrorsDoNotLeakSettings(t *testing.T) {
	h := &resolverSettingsTestHandler{err: errors.New("private.example. secret endpoint")}
	s := startMutationTestServer(t, h)
	_, err := NewClient(s.stateDir).CallWithParams(context.Background(), api.MethodResolverList, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "unavailable") || strings.Contains(err.Error(), "private.example") || strings.Contains(err.Error(), "secret endpoint") {
		t.Fatal("private error escaped", err)
	}
}
