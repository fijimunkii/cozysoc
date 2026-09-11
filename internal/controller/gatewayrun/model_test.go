package gatewayrun

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestAuditEventRejectsUnknownOrContradictoryFields(t *testing.T) {
	c, _, log, _ := fixture(t)
	r := prepare(t, c)
	if _, err := c.Run(context.Background(), r.Ticket, true); err != nil {
		t.Fatal(err)
	}
	good := log.copy()[0]
	for name, edit := range map[string]func(*Event){
		"schema": func(e *Event) { e.SchemaVersion++ }, "run id": func(e *Event) { e.RunID = "secret" }, "digest": func(e *Event) { e.SelectionDigest = "bad" },
		"profile": func(e *Event) { e.Profile = "shell" }, "scope": func(e *Event) { e.ScopeID = "<script>" }, "interface": func(e *Event) { e.InterfaceName = "en0;id" },
		"index": func(e *Event) { e.InterfaceIndex = 0 }, "time": func(e *Event) { e.At = time.Time{} }, "self": func(e *Event) { e.Source = e.Target },
		"target": func(e *Event) { e.Target = "8.8.8.8" }, "source": func(e *Event) { e.Source = "gateway.local" },
		"state": func(e *Event) { e.State = "healthy" }, "early outcome": func(e *Event) { e.Outcome = "completed" }, "early reason": func(e *Event) { e.Reason = "arbitrary" },
		"terminal without outcome": func(e *Event) { e.State = "finished" },
		"unknown result":           func(e *Event) { e.State = "finished"; e.Outcome = "internet-up" },
		"arbitrary error":          func(e *Event) { e.State = "finished"; e.Outcome = "failed"; e.Reason = "private-socket-error" },
		"contradictory success":    func(e *Event) { e.State = "finished"; e.Outcome = "completed"; e.Reason = "execution-error" },
	} {
		t.Run(name, func(t *testing.T) {
			e := good
			edit(&e)
			if ValidateEvent(e) == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
}
func TestRevalidationFailurePanicAndCancellationConsumeConsent(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel"} {
		c, _, log, calls := fixture(t)
		r := prepare(t, c)
		ctx, cancel := context.WithCancel(context.Background())
		c.deps.Preflight = func(context.Context, netip.Addr) (Selection, error) {
			if mode == "panic" {
				panic("private-preflight-panic")
			}
			if mode == "cancel" {
				cancel()
			}
			return Selection{}, errors.New("private-route-error")
		}
		result, err := c.Run(ctx, r.Ticket, true)
		cancel()
		if err == nil || calls.Load() != 0 || strings.Contains(err.Error(), "private") {
			t.Fatalf("%s: %+v %v", mode, result, err)
		}
		events := log.copy()
		if len(events) != 2 || events[1].State != "finished" {
			t.Fatal(events)
		}
		if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
			t.Fatal(err)
		}
	}
}

func TestClockFailureAfterAdmissionDoesNotInventTerminalTime(t *testing.T) {
	c, clock, log, _ := fixture(t)
	r := prepare(t, c)
	c.deps.Executor = executorFunc(func(context.Context, Selection) error { clock.add(-time.Second); return nil })
	result, err := c.Run(context.Background(), r.Ticket, true)
	if !errors.Is(err, ErrClock) || result != (Result{}) || len(log.copy()) != 2 {
		t.Fatalf("invented trustworthy terminal event: %+v %v", result, err)
	}
}

type panicAuditor struct{}

func (panicAuditor) InsertGatewayRunAudit(context.Context, Event) error {
	panic("private-database-panic")
}
func TestAuditPanicStopsBeforeExecution(t *testing.T) {
	c, _, _, calls := fixture(t)
	r := prepare(t, c)
	c.deps.Auditor = panicAuditor{}
	if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrAudit) || calls.Load() != 0 {
		t.Fatal(err)
	}
}
