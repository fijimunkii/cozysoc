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
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

// Real Unix socket/peer/secret/protocol, synthetic metadata and packet-free sender.
type dnsSessionHandler struct {
	testHandler
	control *resolverrun.Control
}

func (h dnsSessionHandler) ResolverCheckControl() (*resolverrun.Control, error) {
	return h.control, nil
}

type dnsSessionExecutor func(context.Context, resolverrun.Request) (networkquality.ResolverMeasurement, error)

func (f dnsSessionExecutor) ExecuteResolver(ctx context.Context, s resolverrun.Request) (networkquality.ResolverMeasurement, error) {
	return f(ctx, s)
}

type dnsSessionAudit struct {
	mu           sync.Mutex
	events       []resolverrun.Event
	failTerminal bool
	finished     chan struct{}
}

func (a *dnsSessionAudit) InsertResolverRunAudit(_ context.Context, e resolverrun.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := resolverrun.ValidateEvent(e); err != nil {
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
func (a *dnsSessionAudit) count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.events) }

func dnsSessionFixture(t *testing.T, execute dnsSessionExecutor, verifier peerVerifier, failAudit bool) (*Server, *resolverrun.Control, *dnsSessionAudit, *atomic.Int32, *atomic.Int32, context.CancelFunc) {
	t.Helper()
	calls, preflights := &atomic.Int32{}, &atomic.Int32{}
	audit := &dnsSessionAudit{finished: make(chan struct{}), failTerminal: failAudit}
	if execute == nil {
		execute = func(_ context.Context, s resolverrun.Request) (networkquality.ResolverMeasurement, error) {
			d := s.Selection.Plan.Disclosure()
			now := time.Now().UTC()
			return networkquality.ResolverMeasurement{ID: s.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: now, CompletedAt: now, Request: networkquality.DNSRequestAccepted, Exchange: networkquality.DNSIncomplete}, resolverrun.ErrExecution
		}
	}
	constructed := false
	control, err := resolverrun.New(resolverrun.Dependencies{
		Now: func() time.Time {
			if !constructed {
				return time.Now().Add(-resolverrun.RunInterval)
			}
			return time.Now()
		},
		Auditor: audit,
		Preflight: func(_ context.Context, id string) (resolverrun.Selection, error) {
			preflights.Add(1)
			now := time.Now().UTC()
			plan, err := resolverplan.New(resolverplan.Binding{Observer: networkquality.Observer{ScopeID: "scope.fixture", SensorID: "fixture", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.168.50.0/24"}, Source: netip.MustParseAddr("192.168.50.23")}, resolverplan.Configuration{Selection: networkquality.ResolverSelection{ID: id, ResolverID: "resolver-v1", QueryID: "query-v1", Family: networkquality.FamilyIPv4, Transport: networkquality.DNSUDP, QueryType: networkquality.DNSQueryA, Expect: networkquality.DNSExpectAnswer}, Endpoint: netip.MustParseAddrPort("192.168.50.1:53"), Name: "test.example.", DestinationScope: resolverplan.EnrolledPrefix}, now)
			return resolverrun.Selection{Plan: plan, RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}, err
		},
		Executor: dnsSessionExecutor(func(ctx context.Context, s resolverrun.Request) (networkquality.ResolverMeasurement, error) {
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
	server, err := newServer(dir, dnsSessionHandler{control: control}, nil, verifier)
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

func dnsSessionRequest(secret string, params json.RawMessage) api.Request {
	return api.Request{Version: api.Version, ID: "resolver-check", Method: api.MethodResolverCheck, Auth: secret, Params: params}
}
func openDNSReview(t *testing.T, s *Server) (net.Conn, *bufio.Reader, api.ResolverCheckReview) {
	t.Helper()
	conn, err := net.Dial("unix", s.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(dnsSessionRequest(s.secret, json.RawMessage(`{"selection_id":"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReaderSize(conn, gatewayFrameLimit+1)
	raw, err := readResolverResponse(reader, "resolver-check")
	if err != nil {
		t.Fatal(err)
	}
	var review api.ResolverCheckReview
	if json.Unmarshal(raw, &review) != nil || validateResolverReview(review, "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now()) != nil {
		t.Fatal("invalid review")
	}
	return conn, reader, review
}
func waitDNSSession(t *testing.T, f func() bool) {
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

func TestResolverConsentAndImmutableReview(t *testing.T) {
	s, _, audit, calls, _, _ := dnsSessionFixture(t, nil, nil, false)
	callbackCalls := 0
	got, err := NewClient(s.stateDir).CheckResolver(context.Background(), "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(ctx context.Context, r api.ResolverCheckReview) (bool, error) {
		callbackCalls++
		if _, ok := ctx.Deadline(); !ok || audit.count() != 0 || calls.Load() != 0 {
			t.Fatal("review granted authority")
		}
		if r.Budget.MaxSendCalls != 1 || r.Source != "192.168.50.23" || r.Binding.InterfaceIndex != 7 {
			t.Fatal("missing review")
		}
		r.SelectionID = "selection.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		r.Binding.Prefixes[0] = "10.0.0.0/8"
		r.Budget.MaxSendCalls = 1000
		return true, nil
	})
	if err != nil || callbackCalls != 1 || got.Outcome != "failed" || got.FailureCode != "execution_failed" || got.Measurement == nil || got.Measurement.Exchange != networkquality.DNSIncomplete || got.Measurement.Request != networkquality.DNSRequestAccepted || calls.Load() != 1 || audit.count() != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Review.SelectionID != "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || got.Review.Binding.Prefixes[0] != "192.168.50.0/24" {
		t.Fatal("client retargeted authority")
	}
	_, err = NewClient(s.stateDir).CheckResolver(context.Background(), "selection.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", func(context.Context, api.ResolverCheckReview) (bool, error) {
		t.Error("cooldown issued review")
		return true, nil
	})
	var response *ResponseError
	if !errors.As(err, &response) || response.Code != "cooldown" || calls.Load() != 1 {
		t.Fatal("missing global cooldown", err)
	}
}

func TestResolverDeclineAndAbandon(t *testing.T) {
	s, c, audit, calls, _, _ := dnsSessionFixture(t, nil, nil, false)
	got, err := NewClient(s.stateDir).CheckResolver(context.Background(), "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(context.Context, api.ResolverCheckReview) (bool, error) { return false, nil })
	if err != nil || got.Outcome != "declined" || got.RunID != "" || got.Measurement != nil {
		t.Fatalf("%+v %v", got, err)
	}
	conn, _, old := openDNSReview(t, s)
	conn.Close()
	waitDNSSession(t, func() bool {
		r, err := c.Prepare(context.Background(), "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		if err != nil {
			return false
		}
		c.Discard(r.Ticket)
		return true
	})
	conn, reader, newReview := openDNSReview(t, s)
	if old.Challenge == newReview.Challenge {
		t.Fatal("reused challenge")
	}
	approve := true
	_ = json.NewEncoder(conn).Encode(api.ResolverCheckDecision{Challenge: old.Challenge, Approve: &approve})
	if _, err := readResolverResponse(reader, "resolver-check"); err == nil {
		t.Fatal("cross-connection approval accepted")
	}
	if calls.Load() != 0 || audit.count() != 0 {
		t.Fatal("decline or replay executed")
	}
}

func TestResolverDecisionGrammar(t *testing.T) {
	for name, body := range map[string]string{
		"missing-approval":  `{"challenge":%q}`,
		"null":              `{"challenge":%q,"approve":null}`,
		"string":            `{"challenge":%q,"approve":"true"}`,
		"case":              `{"challenge":%q,"Approve":true}`,
		"duplicate":         `{"challenge":%q,"approve":false,"approve":true}`,
		"escaped-duplicate": `{"challenge":%q,"approve":false,"\u0061pprove":true}`,
		"override":          `{"challenge":%q,"approve":true,"selection_id":"selection.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
		"extra-value":       `{"challenge":%q,"approve":true} true`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _, audit, calls, _, _ := dnsSessionFixture(t, nil, nil, false)
			conn, reader, r := openDNSReview(t, s)
			_, _ = fmt.Fprintf(conn, body+"\n", r.Challenge)
			if _, err := readResolverResponse(reader, "resolver-check"); err == nil {
				t.Fatal("invalid decision accepted")
			}
			if calls.Load() != 0 || audit.count() != 0 {
				t.Fatal("invalid consent executed")
			}
		})
	}
}

func TestResolverAuthenticationAndStrictRequest(t *testing.T) {
	for _, mode := range []string{"wrong-secret", "unverified-peer", "wrong-uid", "duplicate-target", "case-target", "scope", "null-target", "public", "duplicate-method", "large"} {
		t.Run(mode, func(t *testing.T) {
			var verify peerVerifier
			if mode == "unverified-peer" {
				verify = func(net.Conn) (PeerIdentity, error) { return PeerIdentity{UID: os.Geteuid()}, nil }
			}
			if mode == "wrong-uid" {
				verify = func(net.Conn) (PeerIdentity, error) { return PeerIdentity{UID: os.Geteuid() + 1, Verified: true}, nil }
			}
			s, _, audit, calls, preflights, _ := dnsSessionFixture(t, nil, verify, false)
			params := `{"selection_id":"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
			switch mode {
			case "duplicate-target":
				params = `{"selection_id":"selection.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","selection_id":"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
			case "case-target":
				params = `{"Selection_ID":"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
			case "scope":
				params = `{"selection_id":"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scope_id":"scope.fixture"}`
			case "null-target":
				params = `{"selection_id":null}`
			case "public":
				params = `{"selection_id":"8.8.8.8"}`
			case "large":
				params = `{"selection_id":"` + strings.Repeat("1", 9000) + `"}`
			}
			secret := s.secret
			if mode == "wrong-secret" {
				secret = "wrong"
			}
			raw, _ := json.Marshal(dnsSessionRequest(secret, json.RawMessage(params)))
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

func TestResolverDisconnectCancelsRun(t *testing.T) {
	entered := make(chan struct{})
	execute := dnsSessionExecutor(func(ctx context.Context, s resolverrun.Request) (networkquality.ResolverMeasurement, error) {
		close(entered)
		<-ctx.Done()
		return networkquality.ResolverMeasurement{}, ctx.Err()
	})
	s, _, audit, calls, _, _ := dnsSessionFixture(t, execute, nil, false)
	conn, _, r := openDNSReview(t, s)
	approve := true
	_ = json.NewEncoder(conn).Encode(api.ResolverCheckDecision{Challenge: r.Challenge, Approve: &approve})
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

func TestResolverServerCancelClosesReview(t *testing.T) {
	s, _, audit, calls, _, cancel := dnsSessionFixture(t, nil, nil, false)
	conn, reader, _ := openDNSReview(t, s)
	cancel()
	if _, err := readResolverResponse(reader, "resolver-check"); err == nil {
		t.Fatal("server cancellation left session open")
	}
	conn.Close()
	if audit.count() != 0 || calls.Load() != 0 {
		t.Fatal("cancel authorized work")
	}
}

func TestResolverUnconfirmedAuditHasNoResult(t *testing.T) {
	s, _, audit, calls, _, _ := dnsSessionFixture(t, nil, nil, true)
	got, err := NewClient(s.stateDir).CheckResolver(context.Background(), "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(context.Context, api.ResolverCheckReview) (bool, error) { return true, nil })
	var response *ResponseError
	if !errors.As(err, &response) || response.Code != "audit_unconfirmed" || got.RunID != "" || got.Measurement != nil || audit.count() != 3 || calls.Load() != 1 {
		t.Fatalf("published uncertain audit: %+v %v", got, err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("raw error leaked")
	}
}
