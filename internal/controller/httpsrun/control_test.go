package httpsrun

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

	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *testClock) add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.at = c.at.Add(d) }

type executeFunc func(context.Context, Request) (nq.HTTPSMeasurement, error)

func (f executeFunc) ExecuteHTTPS(ctx context.Context, r Request) (nq.HTTPSMeasurement, error) {
	return f(ctx, r)
}

type testAudit struct {
	mu     sync.Mutex
	events []Event
	hook   func(context.Context, Event) error
}

func (a *testAudit) InsertHTTPSRunAudit(ctx context.Context, e Event) error {
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
func fixturePlan(now time.Time, name string) (httpsplan.Plan, error) {
	return httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint-v1", RequestID: "request-v1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: name, RequestTarget: "/check", DestinationPolicy: httpsplan.ExactEndpoint}, now)
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
	h := &harness{clock: &testClock{at: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}, audit: &testAudit{}, name: "private.example"}
	c, e := New(Dependencies{Now: h.clock.now, Auditor: h.audit, Preflight: func(ctx context.Context, id string) (Selection, error) {
		h.preflights.Add(1)
		if h.preflight != nil {
			return h.preflight(ctx, id)
		}
		p, e := fixturePlan(h.clock.now(), h.name)
		return Selection{Plan: p, RouteObservedAt: h.clock.now(), RouteFreshUntil: h.clock.now().Add(httpsplan.ReviewLifetime)}, e
	}, Executor: executeFunc(func(ctx context.Context, r Request) (nq.HTTPSMeasurement, error) {
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
func (h *harness) response(r Request) nq.HTTPSMeasurement {
	d := r.Selection.Plan.Disclosure()
	start := h.clock.now()
	h.clock.add(10 * time.Millisecond)
	duration := 10 * time.Millisecond
	return nq.HTTPSMeasurement{ID: r.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: h.clock.now(), Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTime: &duration}
}
func (h *harness) prepare(t *testing.T) Review {
	t.Helper()
	r, e := h.control.Prepare(context.Background(), "https1")
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

func TestOneShotDurableOrderingAndHTTPSOutcome(t *testing.T) {
	h := setup(t)
	h.clock.add(-RunInterval)
	_, e := h.control.Prepare(context.Background(), "https1")
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
	h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
		events := h.audit.all()
		if len(events) != 2 || events[0].State != "authorized" || events[1].State != "admitted" {
			t.Fatal("executed before durable admission")
		}
		return h.response(req), nil
	}
	result, e := h.control.Run(context.Background(), r.Ticket, true)
	if e != nil || result.Outcome != "completed" || result.Sample == nil || result.Sample.StatusCode != 503 {
		t.Fatalf("HTTP 503 conflated with execution failure: %+v %v", result, e)
	}
	_, e = h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, ErrReview)
	_, e = h.control.Prepare(context.Background(), "https1")
	expectError(t, e, ErrCooldown)
	events := h.audit.all()
	if len(events) != 3 || events[2].Measurement.StatusCode != 503 || h.calls.Load() != 1 {
		t.Fatal("incorrect evidence")
	}
	result.Sample.StatusCode = 0
	*result.Sample.ResponseTime = 0
	if events[2].Measurement.StatusCode != 503 || *events[2].Measurement.ResponseTimeNanoseconds == 0 {
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
				h.name = "other.example"
			case "expiry":
				h.clock.add(httpsplan.ReviewLifetime)
			case "stale-route":
				h.preflight = func(ctx context.Context, id string) (Selection, error) {
					p, e := fixturePlan(h.clock.now(), h.name)
					return Selection{Plan: p, RouteObservedAt: h.clock.now().Add(-time.Second), RouteFreshUntil: h.clock.now().Add(29 * time.Second)}, e
				}
			case "foreign-id":
				h.preflight = func(ctx context.Context, id string) (Selection, error) {
					p, e := fixturePlan(h.clock.now(), h.name)
					d := p.Disclosure()
					d.Configuration.Selection.ID = "https2"
					p, e = httpsplan.New(d.Binding, httpsplan.Configuration(d.Configuration), h.clock.now())
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
				_, e = h.control.Prepare(context.Background(), "https1")
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
						h.clock.add(httpsplan.ReviewLifetime)
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
	for _, kind := range []string{"panic", "zero", "identity", "invalid-stage", "zero-timeout", "missing-timing", "invalid-status", "past", "future", "incomplete-success"} {
		t.Run(kind, func(t *testing.T) {
			h := setup(t)
			r := h.prepare(t)
			h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
				if kind == "panic" {
					panic("private request")
				}
				m := h.response(req)
				switch kind {
				case "zero":
					m = nq.HTTPSMeasurement{}
				case "identity":
					m.ID = "other"
				case "invalid-stage":
					m.Exchange = nq.HTTPSTimeout
					m.Stage = nq.HTTPSTLS
					m.StatusCode = 0
					m.ResponseTime = nil
				case "zero-timeout":
					m.CompletedAt = m.StartedAt
					m.Exchange = nq.HTTPSTimeout
					m.StatusCode = 0
					m.ResponseTime = nil
				case "missing-timing":
					m.ResponseTime = nil
				case "invalid-status":
					m.StatusCode = 600
				case "past":
					m.StartedAt = m.StartedAt.Add(-time.Second)
				case "future":
					m.CompletedAt = m.CompletedAt.Add(time.Hour)
				case "incomplete-success":
					m.Exchange = nq.HTTPSIncomplete
					m.StatusCode = 0
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
		h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
			d := req.Selection.Plan.Disclosure()
			start := h.clock.now()
			h.clock.add(2 * time.Second)
			m := nq.HTTPSMeasurement{ID: req.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: h.clock.now(), Request: nq.HTTPSRequestAccepted, Stage: nq.HTTPSRequest, Exchange: nq.HTTPSTimeout}
			if incomplete {
				m.Request = nq.HTTPSRequestUncertain
				m.Exchange = nq.HTTPSIncomplete
				return m, errors.New("private send error")
			}
			return m, nil
		}
		result, e := h.control.Run(context.Background(), r.Ticket, true)
		if incomplete {
			expectError(t, e, ErrExecution)
			if result.Outcome != "failed" || result.Sample.Request != nq.HTTPSRequestUncertain {
				t.Fatal("uncertain promoted")
			}
		} else if e != nil || result.Outcome != "completed" || result.Sample.Exchange != nq.HTTPSTimeout {
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
	_, e = h.control.Prepare(context.Background(), "https1")
	expectError(t, e, ErrClock)
	if h.calls.Load() != 0 {
		t.Fatal("clock reversal executed")
	}
	h = setup(t)
	h.clock.mu.Lock()
	h.clock.at = time.Unix(0, 1<<63-1).Add(-time.Second)
	h.clock.mu.Unlock()
	_, e = h.control.Prepare(context.Background(), "https1")
	expectError(t, e, ErrClock)
}

func TestShutdownJoinsCanceledExecutor(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nq.HTTPSMeasurement{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, e := h.control.Run(context.Background(), r.Ticket, true); done <- e }()
	<-entered
	_, e := h.control.Prepare(context.Background(), "https1")
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
	h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
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
	if _, e := h.control.Prepare(context.Background(), "https1"); !errors.Is(e, ErrPreflight) {
		t.Fatal(e)
	}
	h.preflight = nil
	r := h.prepare(t)
	h.execute = func(context.Context, Request) (nq.HTTPSMeasurement, error) {
		return nq.HTTPSMeasurement{}, panickingError{}
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
	if _, e := c.Prepare(context.Background(), "https1"); !errors.Is(e, ErrUnavailable) {
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
	mutations := []func(*Event){func(e *Event) { e.SchemaVersion++ }, func(e *Event) { e.RunID = "private-name" }, func(e *Event) { e.Profile = "other" }, func(e *Event) { e.Selection.Method = "POST" }, func(e *Event) { e.Observer.SensorID = "" }, func(e *Event) { e.Measurement = nil }, func(e *Event) { e.State = "admitted" }, func(e *Event) { e.Reason = "raw upstream error" }, func(e *Event) { e.At = e.At.Add(-time.Second) }, func(e *Event) { e.Outcome = "indeterminate"; e.Reason = "execution-panic" }}
	for i, mut := range mutations {
		e := base
		mut(&e)
		if ValidateEvent(e) == nil {
			t.Fatalf("accepted contradiction %d", i)
		}
	}
}

func TestIncompleteCleanupMayFinishAfterOriginalReview(t *testing.T) {
	h := setup(t)
	r := h.prepare(t)
	h.clock.add(29 * time.Second)
	h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
		d := req.Selection.Plan.Disclosure()
		start := h.clock.now()
		h.clock.add(1100 * time.Millisecond)
		return nq.HTTPSMeasurement{ID: req.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: h.clock.now(), Request: nq.HTTPSRequestAccepted, Stage: nq.HTTPSRequest, Exchange: nq.HTTPSIncomplete}, context.DeadlineExceeded
	}
	result, e := h.control.Run(context.Background(), r.Ticket, true)
	expectError(t, e, context.DeadlineExceeded)
	if result.Outcome != "canceled" || result.Sample == nil || result.Sample.Exchange != nq.HTTPSIncomplete {
		t.Fatal("lost incomplete cleanup evidence")
	}
}

func TestPhaseTimeoutVersusOverallDeadline(t *testing.T) {
	for _, stage := range []nq.HTTPSStage{nq.HTTPSConnect, nq.HTTPSTLS, nq.HTTPSRequest} {
		for _, elapsed := range []time.Duration{2 * time.Second, OperationTimeout} {
			t.Run(fmt.Sprintf("%s/%s", stage, elapsed), func(t *testing.T) {
				h := setup(t)
				r := h.prepare(t)
				h.execute = func(ctx context.Context, req Request) (nq.HTTPSMeasurement, error) {
					d := req.Selection.Plan.Disclosure()
					start := h.clock.now()
					h.clock.add(elapsed)
					request := nq.HTTPSRequestNotSent
					if stage == nq.HTTPSRequest {
						request = nq.HTTPSRequestAccepted
					}
					return nq.HTTPSMeasurement{ID: req.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: h.clock.now(), Stage: stage, Request: request, Exchange: nq.HTTPSTimeout}, fmt.Errorf("phase: %w", context.DeadlineExceeded)
				}
				result, err := h.control.Run(context.Background(), r.Ticket, true)
				want := "completed"
				if elapsed == OperationTimeout {
					want = "canceled"
					expectError(t, err, context.DeadlineExceeded)
				} else if err != nil {
					t.Fatal(err)
				}
				events := h.audit.all()
				if result.Outcome != want || result.Sample == nil || result.Sample.Exchange != nq.HTTPSTimeout || len(events) != 3 || events[2].Outcome != want || events[2].Measurement.Stage != stage {
					t.Fatalf("lost timeout attribution: %+v %+v", result, events)
				}
				_, err = h.control.Run(context.Background(), r.Ticket, true)
				expectError(t, err, ErrReview)
			})
		}
	}
}
