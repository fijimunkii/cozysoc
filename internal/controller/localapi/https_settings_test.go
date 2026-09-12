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

type httpsSettingsTestHandler struct {
	testHandler
	calls   atomic.Int32
	bounded atomic.Bool
	err     error
}

func (h *httpsSettingsTestHandler) hit(ctx context.Context) {
	h.calls.Add(1)
	deadline, ok := ctx.Deadline()
	h.bounded.Store(ok && time.Until(deadline) <= requestTimeout)
}
func (h *httpsSettingsTestHandler) SaveHTTPS(ctx context.Context, p api.HTTPSSettingsParams) (api.HTTPSSettingsResult, error) {
	h.hit(ctx)
	return api.HTTPSSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.HTTPSSettings{{Settings: p}}}, h.err
}
func (h *httpsSettingsTestHandler) ListHTTPSSettings(ctx context.Context) (api.HTTPSSettingsResult, error) {
	h.hit(ctx)
	return api.HTTPSSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.HTTPSSettings{}}, h.err
}
func (h *httpsSettingsTestHandler) RetireHTTPS(ctx context.Context, p api.HTTPSIDParams) (api.HTTPSRetireResult, error) {
	h.hit(ctx)
	return api.HTTPSRetireResult{SchemaVersion: 1, SelectionID: p.SelectionID, State: "retired"}, h.err
}

func TestHTTPSNativeMethodsAndExactParameters(t *testing.T) {
	h := &httpsSettingsTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	ctx := context.Background()
	id := "https-selection." + strings.Repeat("a", 32)
	p := api.HTTPSSettingsParams{Endpoint: "198.51.100.20:443", ServerName: "private.example", RequestTarget: "/check", Family: "ipv4", Method: "HEAD", ExpectedStatus: 204, DestinationPolicy: "exact-endpoint"}
	for _, tc := range []struct {
		method string
		params any
	}{{api.MethodHTTPSSave, p}, {api.MethodHTTPSList, struct{}{}}, {api.MethodHTTPSRetire, api.HTTPSIDParams{SelectionID: id}}} {
		if _, err := client.CallWithParams(ctx, tc.method, tc.params); err != nil {
			t.Fatal(err)
		}
	}
	if h.calls.Load() != 3 || !h.bounded.Load() {
		t.Fatal("typed methods not bounded")
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"selection_id":null}`, `{"selection_id":"192.0.2.53"}`, `{"selection_id":"` + id + `","approve":true}`, `{"selection_id":"` + id + `","selection_id":"` + id + `"}`, `{"Selection_ID":"` + id + `"}`, `{"selection_id":"` + id + `","endpoint":"192.0.2.53:53"}`} {
		if _, err := client.CallWithParams(ctx, api.MethodHTTPSRetire, json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), "invalid_request") {
			t.Fatal("invalid parameters accepted", err)
		}
	}
	data, _ := json.Marshal(p)
	for _, field := range []string{`"scope_id":"other"`, `"consent":true`, `"source":"192.0.2.1"`, `"selection_id":"old"`, `"budget":1`, `"server_name":"second.example"`} {
		raw := string(data[:len(data)-1]) + "," + field + "}"
		if _, err := client.CallWithParams(ctx, api.MethodHTTPSSave, json.RawMessage(raw)); err == nil {
			t.Fatal("save accepted extra/duplicate field")
		}
	}
	if h.calls.Load() != 3 {
		t.Fatal("invalid parameters reached handler")
	}
	for _, method := range []string{"network-quality.https-run", "network-quality.https-approve"} {
		if _, err := client.Call(ctx, method); err == nil || !strings.Contains(err.Error(), "method_not_found") {
			t.Fatal("implicit execution endpoint", err)
		}
	}
}

func TestHTTPSAuthentication(t *testing.T) {
	h := &httpsSettingsTestHandler{}
	s := startMutationTestServer(t, h)
	for _, method := range []string{api.MethodHTTPSSave, api.MethodHTTPSList, api.MethodHTTPSRetire, api.MethodHTTPSPlan} {
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

	if _, err := NewClient(unverified.stateDir).CallWithParams(context.Background(), api.MethodHTTPSList, struct{}{}); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatal("unverified identity accepted", err)
	}
}

func TestHTTPSNativeErrorsDoNotLeakSettings(t *testing.T) {
	h := &httpsSettingsTestHandler{err: errors.New("private.example. secret endpoint")}
	s := startMutationTestServer(t, h)
	_, err := NewClient(s.stateDir).CallWithParams(context.Background(), api.MethodHTTPSList, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "unavailable") || strings.Contains(err.Error(), "private.example") || strings.Contains(err.Error(), "secret endpoint") {
		t.Fatal("private error escaped", err)
	}
}

func TestHTTPSSettingsRejectNullAndWrongTypedFields(t *testing.T) {
	h := &httpsSettingsTestHandler{}
	s := startMutationTestServer(t, h)
	c := NewClient(s.stateDir)
	ctx := context.Background()
	for _, raw := range []string{`null`, `[]`, `{"scope_id":"other"}`, `{"approve":true}`} {
		if _, err := c.CallWithParams(ctx, api.MethodHTTPSList, json.RawMessage(raw)); err == nil {
			t.Fatal("list accepted parameters")
		}
	}
	base := `{"endpoint":"198.51.100.20:443","server_name":"private.example","request_target":"/check","family":"ipv4","method":"HEAD","expected_status":204,"destination_policy":"exact-endpoint"}`
	for _, value := range []string{`null`, `"204"`, `204.5`, `true`, `{}`, `1e99`} {
		raw := strings.Replace(base, `"expected_status":204`, `"expected_status":`+value, 1)
		if _, err := c.CallWithParams(ctx, api.MethodHTTPSSave, json.RawMessage(raw)); err == nil {
			t.Fatal("save accepted invalid status type")
		}
	}
	for _, raw := range []string{strings.Replace(base, `"server_name":"private.example",`, "", 1), strings.Replace(base, `"server_name":"private.example"`, `"server_name":null`, 1), strings.Replace(base, `"server_name"`, `"Server_Name"`, 1)} {
		if _, err := c.CallWithParams(ctx, api.MethodHTTPSSave, json.RawMessage(raw)); err == nil {
			t.Fatal("save accepted missing or aliased field")
		}
	}
	if h.calls.Load() != 0 {
		t.Fatal("invalid settings reached handler")
	}
}

func (h *httpsSettingsTestHandler) PreviewHTTPS(ctx context.Context, p api.HTTPSIDParams) (api.HTTPSPlan, error) {
	h.hit(ctx)
	return api.HTTPSPlan{SchemaVersion: 1, Mode: "preview-only"}, h.err
}

func TestHTTPSPreviewExactReferenceAndAuthentication(t *testing.T) {
	h := &httpsSettingsTestHandler{}
	s := startMutationTestServer(t, h)
	c := NewClient(s.stateDir)
	ctx := context.Background()
	id := "https-selection." + strings.Repeat("a", 32)
	if _, err := c.CallWithParams(ctx, api.MethodHTTPSPlan, api.HTTPSIDParams{SelectionID: id}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{}`, `{"selection_id":null}`, `{"selection_id":"` + id + `","approve":true}`, `{"selection_id":"` + id + `","source":"192.0.2.10"}`, `{"selection_id":"` + id + `","selection_id":"` + id + `"}`, `{"Selection_ID":"` + id + `"}`, `{"selection_id":"192.0.2.1"}`} {
		if _, err := c.CallWithParams(ctx, api.MethodHTTPSPlan, json.RawMessage(raw)); err == nil {
			t.Fatal("accepted preview authority")
		}
	}
	if h.calls.Load() != 1 {
		t.Fatal("invalid preview reached handler")
	}
}
