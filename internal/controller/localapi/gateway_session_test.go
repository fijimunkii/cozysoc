package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// Real Unix socket/peer/secret/protocol, synthetic metadata and packet-free sender.
type sessionHandler struct {
	testHandler
	control *gatewayrun.Control
}

func (h sessionHandler) GatewayCheckControl() (*gatewayrun.Control, error) { return h.control, nil }

type sessionExecutor func(context.Context, gatewayrun.Selection) (gatewayicmp.Sample, error)

func (f sessionExecutor) ExecuteGateway(ctx context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
	return f(ctx, s)
}

type sessionAudit struct {
	mu           sync.Mutex
	events       []gatewayrun.Event
	failTerminal bool
	finished     chan struct{}
}

func (a *sessionAudit) InsertGatewayRunAudit(_ context.Context, e gatewayrun.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := gatewayrun.ValidateEvent(e); err != nil {
		return err
	}
	a.events = append(a.events, e)
	if e.State == "finished" {
		defer close(a.finished)
		if a.failTerminal {
			return errors.New("private uncertain database acknowledgement")
		}
	}
	return nil
}
func (a *sessionAudit) count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.events) }

func sessionFixture(t *testing.T, execute sessionExecutor, verifier peerVerifier, failAudit bool) (*Server, *gatewayrun.Control, *sessionAudit, *atomic.Int32, *atomic.Int32, context.CancelFunc) {
	t.Helper()
	calls, preflights := &atomic.Int32{}, &atomic.Int32{}
	audit := &sessionAudit{finished: make(chan struct{}), failTerminal: failAudit}
	if execute == nil {
		execute = func(_ context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
			return gatewayicmp.Sample{ScopeID: s.Plan.Binding.ScopeID, InterfaceName: s.Plan.Binding.InterfaceName, InterfaceIndex: s.Plan.Binding.InterfaceIndex,
				Target: s.Plan.Target, Source: s.Source, StartedAt: time.Now().UTC(), SendCalls: 1, AcceptedRequests: 1}, gatewayicmp.ErrUnavailable
		}
	}
	constructed := false
	control, err := gatewayrun.New(gatewayrun.Dependencies{
		Now: func() time.Time {
			if !constructed {
				return time.Now().Add(-gatewayrun.RunInterval)
			}
			return time.Now()
		},
		Auditor: audit,
		Preflight: func(_ context.Context, target netip.Addr) (gatewayrun.Selection, error) {
			preflights.Add(1)
			now := time.Now().UTC()
			plan, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{ScopeID: "scope.fixture", InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}, target.String(), now)
			return gatewayrun.Selection{Plan: plan, Source: netip.MustParseAddr("192.168.50.23"), RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}, err
		},
		Executor: sessionExecutor(func(ctx context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
			calls.Add(1)
			return execute(ctx, s)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	constructed = true
	dir, err := os.MkdirTemp("", "cz-consent-")
	if err != nil {
		t.Fatal(err)
	}
	if verifier == nil {
		verifier = verifyPeer
	}
	server, err := newServer(dir, sessionHandler{control: control}, nil, verifier)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = control.Shutdown(context.Background())
		_ = server.Close()
		<-done
		_ = os.RemoveAll(dir)
	})
	return server, control, audit, calls, preflights, cancel
}

func sessionRequest(secret string, params json.RawMessage) api.Request {
	return api.Request{Version: api.Version, ID: "gateway-check", Method: api.MethodGatewayCheck, Auth: secret, Params: params}
}
func openReview(t *testing.T, s *Server) (net.Conn, *bufio.Reader, api.GatewayCheckReview) {
	t.Helper()
	conn, err := net.Dial("unix", s.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(sessionRequest(s.secret, json.RawMessage(`{"target":"192.168.50.1"}`))); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReaderSize(conn, gatewayFrameLimit+1)
	raw, err := readGatewayResponse(reader, "gateway-check")
	if err != nil {
		t.Fatal(err)
	}
	var review api.GatewayCheckReview
	if json.Unmarshal(raw, &review) != nil || validateGatewayReview(review, "192.168.50.1", time.Now()) != nil {
		t.Fatal("invalid review")
	}
	return conn, reader, review
}
func waitSession(t *testing.T, f func() bool) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("session did not settle")
}

func TestGatewayConsentAndImmutableReview(t *testing.T) {
	s, _, audit, calls, _, _ := sessionFixture(t, nil, nil, false)
	callbackCalls := 0
	got, err := NewClient(s.stateDir).CheckGateway(context.Background(), "192.168.50.1", func(ctx context.Context, r api.GatewayCheckReview) (bool, error) {
		callbackCalls++
		if _, ok := ctx.Deadline(); !ok || audit.count() != 0 || calls.Load() != 0 {
			t.Fatal("review granted authority")
		}
		if r.Budget.MaxAttempts != 3 || r.Source != "192.168.50.23" || r.Binding.InterfaceIndex != 7 {
			t.Fatal("missing review")
		}
		r.Target = "192.168.50.2"
		r.Binding.Prefixes[0] = "10.0.0.0/8"
		r.Budget.MaxAttempts = 1000
		return true, nil
	})
	if err != nil || callbackCalls != 1 || got.Outcome != "failed" || got.FailureCode != "execution_failed" || got.Measurement == nil || got.Measurement.Complete || got.Measurement.SendCalls != 1 || calls.Load() != 1 || audit.count() != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Review.Target != "192.168.50.1" || got.Review.Binding.Prefixes[0] != "192.168.50.0/24" {
		t.Fatal("client retargeted authority")
	}
	_, err = NewClient(s.stateDir).CheckGateway(context.Background(), "192.168.50.2", func(context.Context, api.GatewayCheckReview) (bool, error) {
		t.Error("cooldown issued review")
		return true, nil
	})
	var response *ResponseError
	if !errors.As(err, &response) || response.Code != "cooldown" || calls.Load() != 1 {
		t.Fatal("missing global cooldown", err)
	}
}

func TestGatewayDeclineAndAbandon(t *testing.T) {
	s, c, audit, calls, _, _ := sessionFixture(t, nil, nil, false)
	got, err := NewClient(s.stateDir).CheckGateway(context.Background(), "192.168.50.1", func(context.Context, api.GatewayCheckReview) (bool, error) { return false, nil })
	if err != nil || got.Outcome != "declined" || got.RunID != "" || got.Measurement != nil {
		t.Fatalf("%+v %v", got, err)
	}
	conn, _, old := openReview(t, s)
	conn.Close()
	waitSession(t, func() bool {
		r, err := c.Prepare(context.Background(), netip.MustParseAddr("192.168.50.1"))
		if err != nil {
			return false
		}
		c.Discard(r.Ticket)
		return true
	})
	conn, reader, newReview := openReview(t, s)
	if old.Challenge == newReview.Challenge {
		t.Fatal("reused challenge")
	}
	approve := true
	_ = json.NewEncoder(conn).Encode(api.GatewayCheckDecision{Challenge: old.Challenge, Approve: &approve})
	if _, err := readGatewayResponse(reader, "gateway-check"); err == nil {
		t.Fatal("cross-connection approval accepted")
	}
	if calls.Load() != 0 || audit.count() != 0 {
		t.Fatal("decline or replay executed")
	}
}

func TestGatewayDecisionGrammar(t *testing.T) {
	for name, body := range map[string]string{
		"missing-approval":  `{"challenge":%q}`,
		"null":              `{"challenge":%q,"approve":null}`,
		"string":            `{"challenge":%q,"approve":"true"}`,
		"case":              `{"challenge":%q,"Approve":true}`,
		"duplicate":         `{"challenge":%q,"approve":false,"approve":true}`,
		"escaped-duplicate": `{"challenge":%q,"approve":false,"\u0061pprove":true}`,
		"override":          `{"challenge":%q,"approve":true,"target":"192.168.50.2"}`,
		"extra-value":       `{"challenge":%q,"approve":true} true`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _, audit, calls, _, _ := sessionFixture(t, nil, nil, false)
			conn, reader, r := openReview(t, s)
			_, _ = fmt.Fprintf(conn, body+"\n", r.Challenge)
			if _, err := readGatewayResponse(reader, "gateway-check"); err == nil {
				t.Fatal("invalid decision accepted")
			}
			if calls.Load() != 0 || audit.count() != 0 {
				t.Fatal("invalid consent executed")
			}
		})
	}
}

func TestGatewayAuthenticationAndStrictRequest(t *testing.T) {
	for _, mode := range []string{"wrong-secret", "unverified-peer", "wrong-uid", "duplicate-target", "case-target", "scope", "null-target", "public", "duplicate-method", "large"} {
		t.Run(mode, func(t *testing.T) {
			var verify peerVerifier
			if mode == "unverified-peer" {
				verify = func(net.Conn) (PeerIdentity, error) { return PeerIdentity{UID: os.Geteuid()}, nil }
			}
			if mode == "wrong-uid" {
				verify = func(net.Conn) (PeerIdentity, error) { return PeerIdentity{UID: os.Geteuid() + 1, Verified: true}, nil }
			}
			s, _, audit, calls, preflights, _ := sessionFixture(t, nil, verify, false)
			params := `{"target":"192.168.50.1"}`
			switch mode {
			case "duplicate-target":
				params = `{"target":"192.168.50.2","target":"192.168.50.1"}`
			case "case-target":
				params = `{"Target":"192.168.50.1"}`
			case "scope":
				params = `{"target":"192.168.50.1","scope_id":"scope.fixture"}`
			case "null-target":
				params = `{"target":null}`
			case "public":
				params = `{"target":"8.8.8.8"}`
			case "large":
				params = `{"target":"` + strings.Repeat("1", 9000) + `"}`
			}
			secret := s.secret
			if mode == "wrong-secret" {
				secret = "wrong"
			}
			raw, _ := json.Marshal(sessionRequest(secret, json.RawMessage(params)))
			if mode == "duplicate-method" {
				raw = []byte(strings.Replace(string(raw), `"method":`, `"method":"status","method":`, 1))
			}
			conn, err := net.Dial("unix", s.SocketPath())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = conn.Write(append(raw, '\n'))
			var response api.Response
			if err := json.NewDecoder(conn).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Error == nil || len(response.Result) != 0 || calls.Load() != 0 || preflights.Load() != 0 || audit.count() != 0 {
				t.Fatal("invalid request reached authority")
			}
		})
	}
}

func TestGatewayDisconnectCancelsRun(t *testing.T) {
	entered := make(chan struct{})
	execute := sessionExecutor(func(ctx context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
		close(entered)
		<-ctx.Done()
		return gatewayicmp.Sample{}, ctx.Err()
	})
	s, _, audit, calls, _, _ := sessionFixture(t, execute, nil, false)
	conn, _, r := openReview(t, s)
	approve := true
	_ = json.NewEncoder(conn).Encode(api.GatewayCheckDecision{Challenge: r.Challenge, Approve: &approve})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("executor not entered")
	}
	conn.Close()
	select {
	case <-audit.finished:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect left execution running")
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.events) != 3 || audit.events[2].Outcome != "canceled" || calls.Load() != 1 {
		t.Fatal("missing cancellation audit")
	}
}

func TestGatewayServerCancelClosesReview(t *testing.T) {
	s, _, audit, calls, _, cancel := sessionFixture(t, nil, nil, false)
	conn, reader, _ := openReview(t, s)
	cancel()
	if _, err := readGatewayResponse(reader, "gateway-check"); err == nil {
		t.Fatal("server cancellation left session open")
	}
	conn.Close()
	if audit.count() != 0 || calls.Load() != 0 {
		t.Fatal("cancel authorized work")
	}
}

func TestGatewayUnconfirmedAuditHasNoResult(t *testing.T) {
	s, _, audit, calls, _, _ := sessionFixture(t, nil, nil, true)
	got, err := NewClient(s.stateDir).CheckGateway(context.Background(), "192.168.50.1", func(context.Context, api.GatewayCheckReview) (bool, error) { return true, nil })
	var response *ResponseError
	if !errors.As(err, &response) || response.Code != "audit_unconfirmed" || got.RunID != "" || got.Measurement != nil || audit.count() != 3 || calls.Load() != 1 {
		t.Fatalf("published uncertain audit: %+v %v", got, err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("raw error leaked")
	}
}
