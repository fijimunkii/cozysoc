package gatewayrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type fakeClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.value = c.value.Add(d) }

type executorFunc func(context.Context, Selection) error

func (f executorFunc) ExecuteGateway(ctx context.Context, s Selection) error { return f(ctx, s) }

type auditLog struct {
	mu     sync.Mutex
	events []Event
	calls  int
	failAt int
	hook   func(Event)
}

func (a *auditLog) InsertGatewayRunAudit(ctx context.Context, e Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > OperationTimeout {
		return errors.New("unbounded audit")
	}
	if a.hook != nil {
		a.hook(e)
	}
	if a.calls == a.failAt {
		return errors.New("private-database-error")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.events = append(a.events, e)
	return nil
}
func (a *auditLog) copy() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event(nil), a.events...)
}
func selectionAt(now time.Time, target netip.Addr) Selection {
	plan, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{ScopeID: "scope.home", InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24", "fe80::/64"}}, target.String(), now)
	if err != nil {
		panic(err)
	}
	return Selection{Plan: plan, Source: netip.MustParseAddr("192.168.50.23"), RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}
}

var testTarget = netip.MustParseAddr("192.168.50.1")

func fixture(t *testing.T) (*Control, *fakeClock, *auditLog, *atomic.Int32) {
	t.Helper()
	clock := &fakeClock{value: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	audit := &auditLog{}
	calls := &atomic.Int32{}
	c, err := New(Dependencies{Now: clock.now, Auditor: audit, Preflight: func(ctx context.Context, target netip.Addr) (Selection, error) {
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > OperationTimeout {
			t.Error("unbounded preflight")
		}
		return selectionAt(clock.now(), target), nil
	}, Executor: executorFunc(func(ctx context.Context, _ Selection) error {
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > OperationTimeout {
			t.Error("unbounded executor")
		}
		calls.Add(1)
		return nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	clock.add(RunInterval)
	return c, clock, audit, calls
}
func prepare(t *testing.T, c *Control) Review {
	t.Helper()
	r, err := c.Prepare(context.Background(), testTarget)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReviewIsNotConsentAndReturnedFieldsCannotChangeRun(t *testing.T) {
	c, _, audit, calls := fixture(t)
	r := prepare(t, c)
	approved := copySelection(r.Selection)
	if calls.Load() != 0 || len(audit.copy()) != 0 {
		t.Fatal("review authorized work")
	}
	if _, err := c.Prepare(context.Background(), testTarget); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), r.Ticket, false); !errors.Is(err, ErrConsent) {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), Ticket{}, true); !errors.Is(err, ErrReview) {
		t.Fatal(err)
	}
	r.Selection.Plan.Binding.Prefixes[0] = "10.0.0.0/8"
	r.Selection.Plan.Budget.MaxAttempts = 1000
	r.Selection.Source = netip.MustParseAddr("8.8.8.8")
	r.Selection.Plan.Target = netip.MustParseAddr("8.8.8.8")
	c.deps.Executor = executorFunc(func(_ context.Context, s Selection) error {
		if !sameSelection(s, approved) {
			t.Fatal("caller changed stored authority")
		}
		calls.Add(1)
		s.Plan.Binding.Prefixes[0] = "10.0.0.0/8" // Neither executor nor review aliases audit context.
		if got := audit.copy(); len(got) != 2 || got[0].State != "authorized" || got[1].State != "admitted" {
			t.Fatal("executor ran before durable admission")
		}
		return nil
	})
	result, err := c.Run(context.Background(), r.Ticket, true)
	if err != nil || result.Outcome != "completed" || calls.Load() != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
		t.Fatal("approval replayed", err)
	}
	events := audit.copy()
	if len(events) != 3 {
		t.Fatal(events)
	}
	for _, e := range events {
		if ValidateEvent(e) != nil || e.RunID != result.RunID || e.SelectionDigest != selectionDigest(approved) {
			t.Fatal("incorrect audit", e)
		}
	}
	if events[2].Outcome != "completed" {
		t.Fatal(events)
	}
	raw, _ := json.Marshal(r.Ticket)
	formatted := fmt.Sprintf("%v %#v", r.Ticket, r.Ticket)
	if string(raw) != `"[redacted]"` || strings.Contains(formatted, fmt.Sprintf("%x", r.Ticket.key[:])) {
		t.Fatal("ticket exposed")
	}
	auditJSON, _ := json.Marshal(events)
	if strings.Contains(string(auditJSON), fmt.Sprintf("%x", r.Ticket.key[:])) {
		t.Fatal("ticket persisted")
	}
}

func TestExactExpiryAndPreCanceledRunCannotBeReplayed(t *testing.T) {
	for _, age := range []time.Duration{30*time.Second - time.Nanosecond, 30 * time.Second, 31 * time.Second} {
		c, clock, audit, calls := fixture(t)
		r := prepare(t, c)
		clock.add(age)
		if age < 30*time.Second {
			// Test the half-open boundary without pretending real audit I/O and
			// execution can complete in the last nanosecond of a fake clock.
			if !c.pending.live(clock.now()) {
				t.Fatal("review expired before its boundary")
			}
			continue
		}
		_, err := c.Run(context.Background(), r.Ticket, true)
		if !errors.Is(err, ErrReview) || calls.Load() != 0 || len(audit.copy()) != 0 {
			t.Fatal("expired review survived", err)
		}
		if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
			t.Fatal(err)
		}
	}
	c, _, audit, calls := fixture(t)
	r := prepare(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Run(ctx, r.Ticket, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(audit.copy()) != 0 {
		t.Fatal("pre-canceled admission created authority")
	}
}

func TestFreshRevalidationBindsEveryReviewedField(t *testing.T) {
	edits := map[string]func(*Selection){
		"scope":              func(s *Selection) { s.Plan.Binding.ScopeID = "scope.other" },
		"interface":          func(s *Selection) { s.Plan.Binding.InterfaceName = "en1" },
		"index":              func(s *Selection) { s.Plan.Binding.InterfaceIndex++ },
		"prefix":             func(s *Selection) { s.Plan.Binding.Prefixes = append(s.Plan.Binding.Prefixes, "10.0.0.0/8") },
		"source":             func(s *Selection) { s.Source = netip.MustParseAddr("192.168.50.24") },
		"target":             func(s *Selection) { s.Plan.Target = netip.MustParseAddr("192.168.50.2") },
		"budget":             func(s *Selection) { s.Plan.Budget.MaxAttempts++ },
		"stale route":        func(s *Selection) { s.RouteObservedAt = s.RouteObservedAt.Add(-time.Minute) },
		"future route":       func(s *Selection) { s.RouteObservedAt = s.RouteObservedAt.Add(time.Second) },
		"inflated route ttl": func(s *Selection) { s.RouteFreshUntil = s.RouteFreshUntil.Add(time.Minute) },
		"expired plan":       func(s *Selection) { s.Plan.ReviewExpiresAt = s.Plan.CreatedAt },
		"self":               func(s *Selection) { s.Source = s.Plan.Target },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			c, clock, audit, calls := fixture(t)
			r := prepare(t, c)
			c.deps.Preflight = func(context.Context, netip.Addr) (Selection, error) {
				s := selectionAt(clock.now(), testTarget)
				edit(&s)
				return s, nil
			}
			result, err := c.Run(context.Background(), r.Ticket, true)
			if !errors.Is(err, ErrPreflight) || result.Outcome != "blocked" || calls.Load() != 0 {
				t.Fatalf("%+v %v", result, err)
			}
			events := audit.copy()
			if len(events) != 2 || events[1].State != "finished" || events[1].Outcome != "blocked" {
				t.Fatal(events)
			}
			if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
				t.Fatal(err)
			}
		})
	}
	// Reordered canonical prefixes are the same approval, not a new scope.
	c, clock, _, calls := fixture(t)
	r := prepare(t, c)
	c.deps.Preflight = func(context.Context, netip.Addr) (Selection, error) {
		s := selectionAt(clock.now(), testTarget)
		p := s.Plan.Binding.Prefixes
		p[0], p[1] = p[1], p[0]
		return s, nil
	}
	if _, err := c.Run(context.Background(), r.Ticket, true); err != nil || calls.Load() != 1 {
		t.Fatal(err)
	}
}

func TestAuditFailureAtEveryBoundaryLocksWithoutRetry(t *testing.T) {
	for _, fail := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			c, clock, audit, calls := fixture(t)
			r := prepare(t, c)
			audit.failAt = fail
			result, err := c.Run(context.Background(), r.Ticket, true)
			if !errors.Is(err, ErrAudit) || result != (Result{}) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("%+v %v", result, err)
			}
			want := int32(0)
			if fail == 3 {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("executor crossed failed audit", calls.Load())
			}
			audit.failAt = 0
			clock.add(time.Hour)
			if _, err := c.Prepare(context.Background(), testTarget); !errors.Is(err, ErrAudit) {
				t.Fatal("audit lock silently cleared", err)
			}
			if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrAudit) {
				t.Fatal(err)
			}
		})
	}
}

func TestCanceledFailedAndPanickingExecutorsHaveAuditedOutcomes(t *testing.T) {
	for _, mode := range []string{"canceled", "error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			c, _, audit, calls := fixture(t)
			r := prepare(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.deps.Executor = executorFunc(func(context.Context, Selection) error {
				calls.Add(1)
				if mode == "canceled" {
					cancel()
					return nil
				}
				if mode == "panic" {
					panic("private-panic-value")
				}
				return errors.New("private-socket-error")
			})
			result, err := c.Run(ctx, r.Ticket, true)
			want := map[string]string{"canceled": "canceled", "error": "failed", "panic": "indeterminate"}[mode]
			if err == nil || result.Outcome != want || calls.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("%+v %v", result, err)
			}
			events := audit.copy()
			if len(events) != 3 || events[2].Outcome != want {
				t.Fatal("missing terminal audit after cancellation", events)
			}
			if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
				t.Fatal(err)
			}
		})
	}
}

func TestExpiryDuringAuditAndPreflightBlocksExecution(t *testing.T) {
	for _, phase := range []string{"authorized", "admitted", "preflight"} {
		c, clock, audit, calls := fixture(t)
		r := prepare(t, c)
		clock.add(29 * time.Second)
		if phase == "preflight" {
			c.deps.Preflight = func(context.Context, netip.Addr) (Selection, error) {
				clock.add(time.Second)
				return selectionAt(clock.now(), testTarget), nil
			}
		} else {
			audit.hook = func(e Event) {
				if e.State == phase {
					clock.add(time.Second)
				}
			}
		}
		result, err := c.Run(context.Background(), r.Ticket, true)
		if !errors.Is(err, ErrReview) || result.Outcome != "blocked" || calls.Load() != 0 {
			t.Fatalf("%s: %+v %v", phase, result, err)
		}
	}
}

func TestConcurrentReplayAndCanceledWorkerDoNotReleaseReservationEarly(t *testing.T) {
	c, _, audit, calls := fixture(t)
	r := prepare(t, c)
	entered, release := make(chan struct{}), make(chan struct{})
	c.deps.Executor = executorFunc(func(context.Context, Selection) error { calls.Add(1); close(entered); <-release; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Run(ctx, r.Ticket, true); done <- err }()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cancel()
	if _, err := c.Prepare(context.Background(), testTarget); !errors.Is(err, ErrBusy) {
		t.Fatal("abandoned active worker", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(audit.copy()) != 3 {
		t.Fatal("duplicate execution")
	}
}

func TestCooldownIsControllerWideAndRestartCannotRestoreTickets(t *testing.T) {
	c, clock, audit, calls := fixture(t)
	r := prepare(t, c)
	if _, err := c.Run(context.Background(), r.Ticket, true); err != nil {
		t.Fatal(err)
	}
	other := netip.MustParseAddr("192.168.50.2")
	for _, delta := range []time.Duration{0, RunInterval - time.Nanosecond} {
		clock.add(delta)
		if _, err := c.Prepare(context.Background(), other); !errors.Is(err, ErrCooldown) {
			t.Fatal("per-target cooldown bypass", err)
		}
	}
	clock.add(time.Nanosecond)
	next, err := c.Prepare(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(c.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := restarted.Prepare(context.Background(), other); !errors.Is(err, ErrCooldown) {
		t.Fatal("restart removed quiet interval", err)
	}
	if _, err := restarted.Run(context.Background(), next.Ticket, true); !errors.Is(err, ErrReview) {
		t.Fatal("restart restored authority", err)
	}
	c.Close()
	if _, err := c.Run(context.Background(), next.Ticket, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(audit.copy()) != 3 {
		t.Fatal("restart executed")
	}
}

func TestClockRollbackLatchesAndInvalidDependenciesStayUnavailable(t *testing.T) {
	c, clock, _, calls := fixture(t)
	r := prepare(t, c)
	clock.add(-time.Nanosecond)
	if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrClock) {
		t.Fatal(err)
	}
	clock.add(time.Hour)
	if _, err := c.Prepare(context.Background(), testTarget); !errors.Is(err, ErrClock) {
		t.Fatal("clock revival", err)
	}
	if calls.Load() != 0 {
		t.Fatal("bad clock admitted")
	}
	if _, err := New(Dependencies{Now: func() time.Time { return time.Time{} }}); !errors.Is(err, ErrClock) {
		t.Fatal(err)
	}
	for _, dep := range []string{"preflight", "executor", "auditor"} {
		c, _, audit, calls := fixture(t)
		switch dep {
		case "preflight":
			c.deps.Preflight = nil
		case "executor":
			c.deps.Executor = nil
		case "auditor":
			c.deps.Auditor = nil
		}
		if _, err := c.Prepare(context.Background(), testTarget); !errors.Is(err, ErrUnavailable) || calls.Load() != 0 || len(audit.copy()) != 0 {
			t.Fatal("missing dependency accepted", err)
		}
	}
}

func TestPrepareCancellationErrorsAndLateEvidenceNeverIssueTicket(t *testing.T) {
	for _, mode := range []string{"canceled", "error", "panic", "old", "slow", "entropy"} {
		c, clock, audit, calls := fixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		c.deps.Preflight = func(context.Context, netip.Addr) (Selection, error) {
			s := selectionAt(clock.now(), testTarget)
			switch mode {
			case "canceled":
				cancel()
			case "error":
				return Selection{}, errors.New("private-error")
			case "panic":
				panic("private-panic")
			case "old":
				s = selectionAt(clock.now().Add(-time.Second), testTarget)
			case "slow":
				clock.add(6 * time.Second)
			}
			return s, nil
		}
		if mode == "entropy" {
			c.random = strings.NewReader("")
		}
		got, err := c.Prepare(ctx, testTarget)
		cancel()
		if err == nil || !reflect.DeepEqual(got, Review{}) || calls.Load() != 0 || len(audit.copy()) != 0 {
			t.Fatalf("%s issued review: %+v %v", mode, got, err)
		}
	}
}

func TestCloseCancelsActiveRunAndKeepsTerminalAudit(t *testing.T) {
	c, _, audit, _ := fixture(t)
	r := prepare(t, c)
	entered := make(chan struct{})
	done := make(chan error, 1)
	c.deps.Executor = executorFunc(func(ctx context.Context, _ Selection) error { close(entered); <-ctx.Done(); return ctx.Err() })
	go func() { _, err := c.Run(context.Background(), r.Ticket, true); done <- err }()
	<-entered
	c.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	events := audit.copy()
	if len(events) != 3 || events[2].Outcome != "canceled" {
		t.Fatal(events)
	}
}

func TestOriginalReviewExpiryBoundsExecutorDespiteFreshPreflight(t *testing.T) {
	c, clock, audit, calls := fixture(t)
	r := prepare(t, c)
	clock.add(29 * time.Second)
	c.deps.Executor = executorFunc(func(ctx context.Context, fresh Selection) error {
		calls.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Fatal("fresh preflight extended the consumed review deadline")
		}
		if !fresh.Plan.CreatedAt.Equal(clock.now()) || !fresh.Plan.ReviewExpiresAt.After(r.ExpiresAt) {
			t.Fatal("fixture did not acquire newer preflight evidence")
		}
		return nil
	})
	result, err := c.Run(context.Background(), r.Ticket, true)
	if err != nil || result.Outcome != "completed" || calls.Load() != 1 || len(audit.copy()) != 3 {
		t.Fatalf("bounded execution: %+v %v", result, err)
	}
}
