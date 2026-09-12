package resolverrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
)

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *testClock) add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.at = c.at.Add(d) }

type executeFunc func(context.Context, Request) (nq.ResolverMeasurement, error)

func (f executeFunc) ExecuteResolver(ctx context.Context, r Request) (nq.ResolverMeasurement, error) {
	return f(ctx, r)
}

type testAudit struct {
	mu     sync.Mutex
	events []Event
	hook   func(context.Context, Event) error
}

func (a *testAudit) InsertResolverRunAudit(ctx context.Context, e Event) error {
	if a.hook != nil {
		if err := a.hook(ctx, e); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}
func (a *testAudit) all() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event(nil), a.events...)
}
func fixturePlan(now time.Time, name string) (resolverplan.Plan, error) {
	return resolverplan.New(resolverplan.Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, resolverplan.Configuration{Selection: nq.ResolverSelection{ID: "dns1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Endpoint: netip.MustParseAddrPort("192.0.2.53:53"), Name: name, DestinationScope: resolverplan.EnrolledPrefix}, now)
}

type harness struct {
	clock      *testClock
	audit      *testAudit
	control    *Control
	calls      atomic.Int32
	preflights atomic.Int32
	name       string
	execute    executeFunc
	preflight  func(context.Context, string) (Selection, error)
}

func setup(t *testing.T) *harness {
	t.Helper()
	h := &harness{clock: &testClock{at: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}, audit: &testAudit{}, name: "private.example."}
	c, e := New(Dependencies{Now: h.clock.now, Auditor: h.audit, Preflight: func(ctx context.Context, id string) (Selection, error) {
		h.preflights.Add(1)
		if h.preflight != nil {
			return h.preflight(ctx, id)
		}
		p, e := fixturePlan(h.clock.now(), h.name)
		return Selection{Plan: p, RouteObservedAt: h.clock.now(), RouteFreshUntil: h.clock.now().Add(resolverplan.ReviewLifetime)}, e
	}, Executor: executeFunc(func(ctx context.Context, r Request) (nq.ResolverMeasurement, error) {
		h.calls.Add(1)
		if h.execute != nil {
			return h.execute(ctx, r)
		}
		return h.response(r), nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	h.control = c
	t.Cleanup(c.Close)
	h.clock.add(RunInterval)
	return h
}
func (h *harness) response(r Request) nq.ResolverMeasurement {
	d := r.Selection.Plan.Disclosure()
	start := h.clock.now()
	h.clock.add(10 * time.Millisecond)
	duration := 10 * time.Millisecond
	return nq.ResolverMeasurement{ID: r.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: h.clock.now(), Request: nq.DNSRequestAccepted, Exchange: nq.DNSResponseReceived, Reply: &nq.DNSReply{RCode: 2}, ResponseTime: &duration}
}
func (h *harness) prepare(t *testing.T) Review {
	t.Helper()
	r, e := h.control.Prepare(context.Background(), "dns1")
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func expectError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v want %v", err, want)
	}
}

func TestOneShotDurableOrderingAndDNSOutcome(t *testing.T) {
	h := setup(t)
	h.clock.add(-RunInterval)
	_, e := h.control.Prepare(context.Background(), "dns1")
	expectError(t, e, ErrCooldown)
	h.clock.add(RunInterval)
	r := h.prepare(t)
	if len(h.audit.all()) != 0 {
		t.Fatal("review recorded consent")
	}
	_, e = h.control.Run(context.Background(), r.Ticket, false)
	expectError(t, e, ErrConsent)
	_, e = h.control.Run(context.Background(), Ticket{}, true)
	expectError(t, e, ErrReview)
	h.execute = func(ctx context.Context, req Request) (nq.ResolverMeasurement, error) {
		events := h.audit.all()
		if len(events) != 2 || events[0].State != "authorized" || events[1].State != "admitted" {
			t.Fatal("executed before durable admission")
		}
		return h.response(req), nil
	}
	result, e := h.control.Run(context.Background(), r.Ticket, true)
	if e != nil || result.Outcome != "completed" || result.Sample == nil || result.Sample.Reply.RCode != 2 {
		t.Fatalf("SERVFAIL conflated with execution failure: %+v %v", result, e)
	}
	_, e = h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrReview)
	_, e = h.control.Prepare(context.Background(), "dns1")
	expectError(t, e, ErrCooldown)
	events := h.audit.all()
	if len(events) != 3 || events[2].Measurement.Reply.RCode != 2 || h.calls.Load() != 1 {
		t.Fatal("incorrect evidence")
	}
	result.Sample.Reply.RCode = 0
	*result.Sample.ResponseTime = 0
	if events[2].Measurement.Reply.RCode != 2 || *events[2].Measurement.ResponseTimeNanoseconds == 0 {
		t.Fatal("result aliases audit")
	}
	data, _ := json.Marshal(events)
	for _, forbidden := range []string{"private.example", "192.0.2", "ticket", "packet"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("private data persisted")
		}
	}
	encoded, _ := json.Marshal(r.Ticket)
	if strings.Contains(fmt.Sprintf("%#v", r.Ticket), fmt.Sprintf("%x", r.Ticket.key)) || string(encoded) != `"[redacted]"` {
		t.Fatal("ticket leaked")
	}
}

func TestChangedSelectionAndExpiredReviewNeverExecute(t *testing.T) {
	for _, kind := range []string{"name", "expiry", "stale-route", "foreign-id", "preflight-error"} {
		t.Run(kind, func(t *testing.T) {
			h := setup(t)
			r := h.prepare(t)
			switch kind {
			case "name":
				h.name = "other.example."
			case "expiry":
				h.clock.add(resolverplan.ReviewLifetime)
			case "stale-route":
				h.preflight = func(ctx context.Context, id string) (Selection, error) {
					p, e := fixturePlan(h.clock.now(), h.name)
					return Selection{Plan: p, RouteObservedAt: h.clock.now().Add(-time.Second), RouteFreshUntil: h.clock.now().Add(29 * time.Second)}, e
				}
			case "foreign-id":
				h.preflight = func(ctx context.Context, id string) (Selection, error) {
					p, e := fixturePlan(h.clock.now(), h.name)
					d := p.Disclosure()
					d.Configuration.Selection.ID = "dns2"
					p, e = resolverplan.New(d.Binding, d.Configuration, h.clock.now())
					return Selection{Plan: p, RouteObservedAt: h.clock.now(), RouteFreshUntil: h.clock.now().Add(30 * time.Second)}, e
				}
			case "preflight-error":
				h.preflight = func(context.Context, string) (Selection, error) {
					return Selection{}, errors.New("private upstream failure")
				}
			}
			result, e := h.control.Run(context.Background(), r.Ticket, true)
			if e == nil || h.calls.Load() != 0 || result.Sample != nil {
				t.Fatal("invalid review executed")
			}
			_, e = h.control.Run(context.Background(), r.Ticket, true)
			expectError(t, e, ErrReview)
			if kind != "expiry" {
				events := h.audit.all()
				if len(events) != 2 || events[1].Outcome != "blocked" {
					t.Fatal("blocked admission missing audit")
				}
			}
		})
	}
}

func TestAuditFailuresLockControl(t *testing.T) {
	for _, state := range []string{"authorized", "admitted", "finished"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", state, panics), func(t *testing.T) {
				h := setup(t)
				r := h.prepare(t)
				h.audit.hook = func(ctx context.Context, e Event) error {
					if e.State == state {
						if panics {
							panic("private audit detail")
						}
						return errors.New("private audit detail")
					}
					return nil
				}
				result, e := h.control.Run(context.Background(), r.Ticket, true)
				expectError(t, e, ErrAudit)
				if result.Sample != nil || result.RunID != "" {
					t.Fatal("published unconfirmed result")
				}
				if state != "finished" && h.calls.Load() != 0 {
					t.Fatal("executed after failed admission")
				}
				h.clock.add(RunInterval)
				_, e = h.control.Prepare(context.Background(), "dns1")
				expectError(t, e, ErrAudit)
			})
		}
	}
}

func TestCancellationAndDeadlineAfterAdmission(t *testing.T) {
	for _, mode := range []string{"cancel", "elapsed", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			r := h.prepare(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.audit.hook = func(_ context.Context, e Event) error {
				if e.State == "admitted" {
					switch mode {
					case "cancel":
						cancel()
					case "elapsed":
						h.clock.add(OperationTimeout)
					case "expiry":
						h.clock.add(resolverplan.ReviewLifetime)
					}
				}
				return nil
			}
			_, e := h.control.Run(ctx, r.Ticket, true)
			if e == nil || h.calls.Load() != 0 {
				t.Fatal("executed after admission expired")
			}
			if mode != "cancel" && h.audit.all()[len(h.audit.all())-1].State != "finished" {
				t.Fatal("missing terminal event")
			}
		})
	}
}

func TestExecutorPanicAndInvalidEvidence(t *testing.T) {
	for _, kind := range []string{"panic", "zero", "identity", "unaccepted-timeout", "short-timeout", "missing-timing", "extended-rcode", "past", "future", "incomplete-success"} {
		t.Run(kind, func(t *testing.T) {
			h := setup(t)
			r := h.prepare(t)
			h.execute = func(ctx context.Context, req Request) (nq.ResolverMeasurement, error) {
				if kind == "panic" {
					panic("private query")
				}
				m := h.response(req)
				switch kind {
				case "zero":
					m = nq.ResolverMeasurement{}
				case "identity":
					m.ID = "other"
				case "unaccepted-timeout":
					m.Exchange = nq.DNSTimeout
					m.Request = nq.DNSRequestNotSent
					m.Reply = nil
					m.ResponseTime = nil
				case "short-timeout":
					m.Exchange = nq.DNSTimeout
					m.Reply = nil
					m.ResponseTime = nil
				case "missing-timing":
					m.ResponseTime = nil
				case "extended-rcode":
					m.Reply.RCode = 16
				case "past":
					m.StartedAt = m.StartedAt.Add(-time.Second)
				case "future":
					m.CompletedAt = m.CompletedAt.Add(time.Hour)
				case "incomplete-success":
					m.Exchange = nq.DNSIncomplete
					m.Reply = nil
					m.ResponseTime = nil
				}
				return m, nil
			}
			result, e := h.control.Run(context.Background(), r.Ticket, true)
			expectError(t, e, ErrExecution)
			if result.Sample != nil {
				t.Fatal("published invalid evidence")
			}
			events := h.audit.all()
			if len(events) != 3 || events[2].Measurement != nil {
				t.Fatal("audit retained invalid evidence")
			}
		})
	}
}

func TestIncompleteAndTimeoutRemainDistinct(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		h := setup(t)
		r := h.prepare(t)
		h.execute = func(ctx context.Context, req Request) (nq.ResolverMeasurement, error) {
			d := req.Selection.Plan.Disclosure()
			start := h.clock.now()
			h.clock.add(2 * time.Second)
			m := nq.ResolverMeasurement{ID: req.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: h.clock.now(), Request: nq.DNSRequestAccepted, Exchange: nq.DNSTimeout}
			if incomplete {
				m.Request = nq.DNSRequestUncertain
				m.Exchange = nq.DNSIncomplete
				return m, errors.New("private send error")
			}
			return m, nil
		}
		result, e := h.control.Run(context.Background(), r.Ticket, true)
		if incomplete {
			expectError(t, e, ErrExecution)
			if result.Outcome != "failed" || result.Sample.Request != nq.DNSRequestUncertain {
				t.Fatal("uncertain promoted")
			}
		} else if e != nil || result.Outcome != "completed" || result.Sample.Exchange != nq.DNSTimeout {
			t.Fatal("timeout conflated")
		}
	}
}

func TestClockFaultAndDiscard(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	h.control.Discard(Ticket{})
	h.control.Discard(r.Ticket)
	_, e := h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrReview)
	r = h.prepare(t)
	h.clock.add(-time.Nanosecond)
	_, e = h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrClock)
	h.clock.add(time.Hour)
	_, e = h.control.Prepare(context.Background(), "dns1")
	expectError(t, e, ErrClock)
	if h.calls.Load() != 0 {
		t.Fatal("clock reversal executed")
	}
	h = setup(t)
	h.clock.mu.Lock()
	h.clock.at = time.Unix(0, 1<<63-1).Add(-time.Second)
	h.clock.mu.Unlock()
	_, e = h.control.Prepare(context.Background(), "dns1")
	expectError(t, e, ErrClock)
}

func TestShutdownJoinsCanceledExecutor(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.execute = func(ctx context.Context, req Request) (nq.ResolverMeasurement, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nq.ResolverMeasurement{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, e := h.control.Run(context.Background(), r.Ticket, true); done <- e }()
	<-entered
	_, e := h.control.Prepare(context.Background(), "dns1")
	expectError(t, e, ErrBusy)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expectError(t, h.control.Shutdown(ctx), context.Canceled)
	_, e = h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrUnavailable)
	close(release)
	expectError(t, <-done, context.Canceled)
	if e := h.control.Shutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
	events := h.audit.all()
	if len(events) != 3 || events[2].Outcome != "canceled" {
		t.Fatal("shutdown lost terminal audit")
	}
}

func TestConcurrentReplayAndOriginalDeadline(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	h.clock.add(29 * time.Second)
	entered, release := make(chan struct{}), make(chan struct{})
	h.execute = func(ctx context.Context, req Request) (nq.ResolverMeasurement, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("fresh preflight extended original approval")
		}
		close(entered)
		<-release
		return h.response(req), nil
	}
	done := make(chan error, 1)
	go func() { _, e := h.control.Run(context.Background(), r.Ticket, true); done <- e }()
	<-entered
	_, e := h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrReview)
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if h.calls.Load() != 1 {
		t.Fatal("replayed execution")
	}
}

func TestShutdownJoinsTerminalAudit(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.audit.hook = func(ctx context.Context, e Event) error {
		if e.State == "finished" {
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, e := h.control.Run(context.Background(), r.Ticket, true); done <- e }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expectError(t, h.control.Shutdown(ctx), context.Canceled)
	select {
	case <-h.control.drained:
		t.Fatal("shutdown released storage before audit returned")
	default:
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := h.control.Shutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
}

type panickingError struct{}

func (panickingError) Error() string { return "private error" }
func (panickingError) Is(error) bool { panic("private error classifier") }
func TestFailClosedCollaborators(t *testing.T) {
	h := setup(t)
	before := h.preflights.Load()
	if _, e := h.control.Prepare(context.Background(), "https://private.example"); !errors.Is(e, ErrPreflight) || h.preflights.Load() != before {
		t.Fatal("bad ID reached preflight")
	}
	h.preflight = func(context.Context, string) (Selection, error) { panic("private metadata") }
	if _, e := h.control.Prepare(context.Background(), "dns1"); !errors.Is(e, ErrPreflight) {
		t.Fatal(e)
	}
	h.preflight = nil
	r := h.prepare(t)
	h.execute = func(context.Context, Request) (nq.ResolverMeasurement, error) {
		return nq.ResolverMeasurement{}, panickingError{}
	}
	result, e := h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrExecution)
	if result.Outcome != "indeterminate" || result.Sample != nil {
		t.Fatal("panic escaped boundary")
	}
	c, e := New(Dependencies{})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e := c.Prepare(context.Background(), "dns1"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("missing collaborator enabled control", e)
	}
	if _, e := New(Dependencies{Now: func() time.Time { return time.Time{} }}); !errors.Is(e, ErrClock) {
		t.Fatal("invalid clock accepted", e)
	}
}

func TestValidateEventRejectsContradictions(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	if _, e := h.control.Run(context.Background(), r.Ticket, true); e != nil {
		t.Fatal(e)
	}
	base := h.audit.all()[2]
	mutations := []func(*Event){func(e *Event) { e.SchemaVersion++ }, func(e *Event) { e.RunID = "private-name" }, func(e *Event) { e.Profile = "other" }, func(e *Event) { e.Selection.Transport = nq.DNSTCP }, func(e *Event) { e.Observer.SensorID = "" }, func(e *Event) { e.Measurement = nil }, func(e *Event) { e.State = "admitted" }, func(e *Event) { e.Reason = "raw upstream error" }, func(e *Event) { e.At = e.At.Add(-time.Second) }, func(e *Event) { e.Outcome = "indeterminate"; e.Reason = "execution-panic" }}
	for i, mut := range mutations {
		e := base
		mut(&e)
		if ValidateEvent(e) == nil {
			t.Fatalf("accepted contradiction %d", i)
		}
	}
}
