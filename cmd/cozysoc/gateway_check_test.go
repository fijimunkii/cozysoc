package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

type promptClient func(context.Context, string, func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error)

func (f promptClient) CheckGateway(ctx context.Context, target string, confirm func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
	return f(ctx, target, confirm)
}

type promptTerminal struct {
	writes            []string
	line              string
	failWrite         int
	flushes, reads    int
	flushErr, readErr error
	onRead            func()
}

func (p *promptTerminal) Write(_ context.Context, s string) error {
	p.writes = append(p.writes, s)
	if len(p.writes) == p.failWrite {
		return errors.New("private output diagnostic")
	}
	return nil
}
func (p *promptTerminal) FlushInput() error { p.flushes++; return p.flushErr }
func (p *promptTerminal) ReadLine(context.Context) (string, error) {
	p.reads++
	if p.onRead != nil {
		p.onRead()
	}
	return p.line, p.readErr
}
func (*promptTerminal) Close() error { return nil }

func promptReview() api.GatewayCheckReview {
	return api.GatewayCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Challenge: strings.Repeat("a", 32), Profile: "gateway-icmp-v1",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Second), Target: "192.168.50.1", Source: "192.168.50.23",
		Binding: api.GatewayPlanBinding{ScopeID: "scope.fixture", InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}},
		Budget:  api.GatewayPlanBudget{MaxAttempts: 3, MinIntervalMS: 1000, AttemptTimeoutMS: 1000, TotalTimeoutMS: 5000, PayloadBytes: 32, MaxICMPRequestBytes: 120, MaxConcurrentRuns: 1, MinRunIntervalMS: 60000}}
}

func TestGatewayPromptRequiresExactFreshApproval(t *testing.T) {
	for _, line := range []string{"\n", "yes\n", "y\n", "CHECK 192.168.50.1\n", " check 192.168.50.1\n", "check 192.168.50.2\n", "check 192.168.50.1", "check 192.168.50.1\nextra", "check 192.168.50.1\n"} {
		t.Run(strings.ReplaceAll(line, "\n", "-"), func(t *testing.T) {
			p := &promptTerminal{line: line}
			calls, approvals := 0, 0
			p.onRead = func() {
				if len(p.writes) != 2 || p.flushes != 1 || !strings.Contains(p.writes[0], "Privacy:") || !strings.Contains(p.writes[1], "default: decline") {
					t.Fatal("input accepted before reviewed disclosure/flush/prompt")
				}
			}
			c := promptClient(func(ctx context.Context, target string, confirm func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
				calls++
				r := promptReview()
				decisionCtx, cancel := context.WithDeadline(ctx, r.ExpiresAt)
				defer cancel()
				approved, err := confirm(decisionCtx, r)
				if err != nil {
					return api.GatewayCheckResult{}, err
				}
				if approved {
					approvals++
					return api.GatewayCheckResult{Outcome: "completed", Review: r, RunID: strings.Repeat("b", 32)}, nil
				}
				return api.GatewayCheckResult{Outcome: "declined", Review: r}, nil
			})
			if err := performGatewayCheck(context.Background(), c, p, "192.168.50.1"); err != nil {
				t.Fatal(err)
			}
			want := 0
			if line == "check 192.168.50.1\n" {
				want = 1
			}
			if calls != 1 || approvals != want || p.reads != 1 {
				t.Fatalf("calls=%d approvals=%d", calls, approvals)
			}
			text := strings.Join(p.writes, "")
			if strings.Contains(text, strings.Repeat("a", 32)) || strings.Contains(text, "scope.fixture") {
				t.Fatal("internal review metadata leaked")
			}
		})
	}
}

func TestGatewayPromptFailuresDoNotApproveOrRetry(t *testing.T) {
	for _, mode := range []string{"review-write", "prompt-write", "ack-write", "flush", "eof", "expired", "canceled-after-input"} {
		t.Run(mode, func(t *testing.T) {
			p := &promptTerminal{line: "check 192.168.50.1\n"}
			calls, approvals := 0, 0
			switch mode {
			case "review-write":
				p.failWrite = 1
			case "prompt-write":
				p.failWrite = 2
			case "ack-write":
				p.failWrite = 3
			case "flush":
				p.flushErr = errors.New("private flush")
			case "eof":
				p.readErr = errors.New("private EOF")
			}
			c := promptClient(func(ctx context.Context, _ string, confirm func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
				calls++
				r := promptReview()
				if mode == "expired" {
					r.ExpiresAt = time.Now().Add(-time.Second)
				}
				dc, cancel := context.WithDeadline(ctx, r.ExpiresAt)
				defer cancel()
				if mode == "canceled-after-input" {
					p.onRead = cancel
				}
				ok, err := confirm(dc, r)
				if ok {
					approvals++
				}
				return api.GatewayCheckResult{}, err
			})
			err := performGatewayCheck(context.Background(), c, p, "192.168.50.1")
			if err == nil || approvals != 0 || calls != 1 || !strings.Contains(err.Error(), "no approval was submitted") || strings.Contains(err.Error(), "private") {
				t.Fatalf("%v calls=%d approvals=%d", err, calls, approvals)
			}
		})
	}
}

func TestGatewayPromptInterruptedAfterApprovalIsUnknown(t *testing.T) {
	p := &promptTerminal{line: "check 192.168.50.1\n"}
	calls := 0
	c := promptClient(func(ctx context.Context, _ string, f func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
		calls++
		r := promptReview()
		dc, cancel := context.WithDeadline(ctx, r.ExpiresAt)
		defer cancel()
		ok, err := f(dc, r)
		if !ok || err != nil {
			t.Fatal("missing approval", err)
		}
		return api.GatewayCheckResult{}, errors.New("private session detail")
	})
	err := performGatewayCheck(context.Background(), c, p, "192.168.50.1")
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "outcome may be unknown") || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}

func TestGatewayPresentationRetainsUnknownAndPartialSemantics(t *testing.T) {
	r := promptReview()
	for _, word := range []string{"EXPERIMENTAL", "gateway role NOT verified", "192.168.50.1", "192.168.50.23", "en0 (index 7)", "192.168.50.0/24", "1000 ms", "5000 ms", "120 bytes", "60000 ms", "32-byte payload", "Privacy:", "not raw packets", "sleep/resume"} {
		if !strings.Contains(gatewayReviewText(r), word) {
			t.Fatal("missing disclosure", word)
		}
	}
	for _, mode := range []string{"absent", "partial", "zero-replies", "zero-rtt", "rtt"} {
		t.Run(mode, func(t *testing.T) {
			result := api.GatewayCheckResult{Review: r, Outcome: "completed", RunID: strings.Repeat("b", 32)}
			if mode != "absent" {
				result.Measurement = &api.GatewayRunMeasurement{StartedAt: r.CreatedAt, Complete: true, SendCalls: 3, AcceptedRequests: 3, Timeouts: 3}
			}
			if mode == "partial" {
				result.Outcome = "canceled"
				result.Measurement.Complete = false
				result.Measurement.SendCalls = 1
				result.Measurement.AcceptedRequests = 1
				result.Measurement.Timeouts = 0
			}
			if mode == "zero-rtt" || mode == "rtt" {
				n := int64(0)
				if mode == "rtt" {
					n = int64(time.Millisecond)
				}
				result.Measurement.MeanRTTNanoseconds = &n
				result.Measurement.Replies = 3
				result.Measurement.Timeouts = 0
			}
			text := gatewayResultText(result)
			if mode == "absent" && !strings.Contains(text, "not measured") {
				t.Fatal(text)
			}
			if mode == "partial" && (!strings.Contains(text, "incomplete") || !strings.Contains(text, "unsent attempts are not timeouts")) {
				t.Fatal(text)
			}
			if (mode == "zero-replies" || mode == "partial") && !strings.Contains(text, "Mean round-trip time: unknown") {
				t.Fatal(text)
			}
			if mode == "zero-rtt" && !strings.Contains(text, "matched replies only): 0s") {
				t.Fatal(text)
			}
			if mode == "rtt" && !strings.Contains(text, "matched replies only): 1ms") {
				t.Fatal(text)
			}
			if strings.Contains(text, "100%") || strings.Contains(text, "internet down") || !strings.Contains(text, "not continuous monitoring") {
				t.Fatal(text)
			}
		})
	}
	for _, code := range []string{"unavailable", "cooldown", "busy", "precondition_failed", "unauthorized", "audit_unconfirmed", "clock_invalid", "secret"} {
		err := gatewayCommandError(&localapi.ResponseError{Code: code, Message: "private-secret"}, false)
		if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "no automatic retry") {
			t.Fatal(err)
		}
	}
}

func TestGatewayCommandRejectsUnattendedFlagsBeforeStateAccess(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	dir := filepath.Join(t.TempDir(), "must-not-exist")
	for _, args := range [][]string{{}, {"--yes", "192.168.50.1"}, {"--approve", "192.168.50.1"}, {"--json", "192.168.50.1"}, {"8.8.8.8"}, {"example.test"}, {"192.168.50.1", "extra"}, {"192.168.50.1"}} {
		full := append([]string{"network-quality-check", "--state-dir", dir}, args...)
		if err := run(context.Background(), full, out, out); err == nil {
			t.Fatal("invalid/nonterminal command accepted", args)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("failed check mutated state", err)
	}
	if err := run(context.Background(), []string{"network-quality-check", "--help"}, out, out); err != nil {
		t.Fatal(err)
	}
}
