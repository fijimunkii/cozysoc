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
)

type dnsPromptClient func(context.Context, string, func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error)

func (f dnsPromptClient) CheckResolver(ctx context.Context, target string, confirm func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error) {
	return f(ctx, target, confirm)
}

func dnsPromptReview() api.ResolverCheckReview {
	return api.ResolverCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Challenge: strings.Repeat("c", 32), Profile: "resolver-udp-v1", SelectionID: "selection." + strings.Repeat("a", 32), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Second), Settings: api.ResolverSettingsParams{Endpoint: "192.168.50.1:53", Name: "test.example.", Family: "ipv4", Transport: "udp", QueryType: "A", Expect: "answer", DestinationScope: "enrolled-prefix"}, Source: "192.168.50.23", Binding: api.GatewayPlanBinding{ScopeID: "scope.fixture", InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}, MayForwardUpstream: true, Budget: api.ResolverPlanBudget{MaxSendCalls: 1, MaxRequestBytes: 30, MaxReplyBytes: 512, MaxReceivedDatagrams: 16, MaxReceiveCalls: 512, ExchangeTimeoutMS: 2000, TotalTimeoutMS: 5000, MaxConcurrentRuns: 1, MinRunIntervalMS: 60000}}
}

func TestResolverPromptRequiresExactFreshApproval(t *testing.T) {
	for _, line := range []string{"\n", "yes\n", "y\n", "CHECK selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", " check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "check selection.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n", "check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nextra", "check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"} {
		t.Run(strings.ReplaceAll(line, "\n", "-"), func(t *testing.T) {
			p := &promptTerminal{line: line}
			calls, approvals := 0, 0
			p.onRead = func() {
				if len(p.writes) != 2 || p.flushes != 1 || !strings.Contains(p.writes[0], "Privacy:") || !strings.Contains(p.writes[1], "default: decline") {
					t.Fatal("input accepted before reviewed disclosure/flush/prompt")
				}
			}
			c := dnsPromptClient(func(ctx context.Context, target string, confirm func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error) {
				calls++
				r := dnsPromptReview()
				decisionCtx, cancel := context.WithDeadline(ctx, r.ExpiresAt)
				defer cancel()
				approved, err := confirm(decisionCtx, r)
				if err != nil {
					return api.ResolverCheckResult{}, err
				}
				if approved {
					approvals++
					return api.ResolverCheckResult{Outcome: "completed", Review: r, RunID: strings.Repeat("b", 32)}, nil
				}
				return api.ResolverCheckResult{Outcome: "declined", Review: r}, nil
			})
			if err := performResolverCheck(context.Background(), c, p, "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
				t.Fatal(err)
			}
			want := 0
			if line == "check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" {
				want = 1
			}
			if calls != 1 || approvals != want || p.reads != 1 {
				t.Fatalf("calls=%d approvals=%d", calls, approvals)
			}
			text := strings.Join(p.writes, "")
			if strings.Contains(text, strings.Repeat("c", 32)) || strings.Contains(text, "scope.fixture") {
				t.Fatal("internal review metadata leaked")
			}
		})
	}
}

func TestResolverPromptFailuresDoNotApproveOrRetry(t *testing.T) {
	for _, mode := range []string{"review-write", "prompt-write", "ack-write", "flush", "eof", "expired", "canceled-after-input"} {
		t.Run(mode, func(t *testing.T) {
			p := &promptTerminal{line: "check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}
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
			c := dnsPromptClient(func(ctx context.Context, _ string, confirm func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error) {
				calls++
				r := dnsPromptReview()
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
				return api.ResolverCheckResult{}, err
			})
			err := performResolverCheck(context.Background(), c, p, "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if err == nil || approvals != 0 || calls != 1 || !strings.Contains(err.Error(), "no approval was submitted") || strings.Contains(err.Error(), "private") {
				t.Fatalf("%v calls=%d approvals=%d", err, calls, approvals)
			}
		})
	}
}

func TestResolverPromptInterruptedAfterApprovalIsUnknown(t *testing.T) {
	p := &promptTerminal{line: "check selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}
	calls := 0
	c := dnsPromptClient(func(ctx context.Context, _ string, f func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error) {
		calls++
		r := dnsPromptReview()
		dc, cancel := context.WithDeadline(ctx, r.ExpiresAt)
		defer cancel()
		ok, err := f(dc, r)
		if !ok || err != nil {
			t.Fatal("missing approval", err)
		}
		return api.ResolverCheckResult{}, errors.New("private session detail")
	})
	err := performResolverCheck(context.Background(), c, p, "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "outcome may be unknown") || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}

func TestResolverCommandRejectsUnattendedFlagsBeforeStateAccess(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	dir := filepath.Join(t.TempDir(), "must-not-exist")
	for _, args := range [][]string{{}, {"--yes", "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"--approve", "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"--json", "selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"8.8.8.8"}, {"example.test"}, {"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "extra"}, {"selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}} {
		full := append([]string{"resolver-check", "--state-dir", dir}, args...)
		if err := run(context.Background(), full, out, out); err == nil {
			t.Fatal("invalid/nonterminal command accepted", args)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("failed check mutated state", err)
	}
	if err := run(context.Background(), []string{"resolver-check", "--help"}, out, out); err != nil {
		t.Fatal(err)
	}
}
