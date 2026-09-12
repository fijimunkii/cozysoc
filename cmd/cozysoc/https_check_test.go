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

type httpsPromptClient func(context.Context, string, func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error)

func (f httpsPromptClient) CheckHTTPS(ctx context.Context, target string, confirm func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error) {
	return f(ctx, target, confirm)
}

func httpsPromptReview() api.HTTPSCheckReview {
	return api.HTTPSCheckReview{SchemaVersion: 1, Mode: "experimental-one-shot", Challenge: strings.Repeat("c", 32), Profile: "selected-https-v1", SelectionID: "https-selection." + strings.Repeat("a", 32), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Second), Settings: api.HTTPSSettingsParams{Endpoint: "198.51.100.20:443", ServerName: "test.example", RequestTarget: "/check", Family: "ipv4", Method: "HEAD", ExpectedStatus: 204, DestinationPolicy: "exact-endpoint"}, Source: "192.168.50.23", Binding: api.GatewayPlanBinding{ScopeID: "scope.fixture", InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}, RequestBytes: "HEAD /check HTTP/1.1\r\nHost: test.example\r\n\r\n", Privacy: []string{"The operator observes the exact request."}}
}

func TestHTTPSPromptRequiresExactFreshApproval(t *testing.T) {
	for _, line := range []string{"\n", "yes\n", "y\n", "CHECK https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", " check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "check https-selection.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n", "check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nextra", "check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"} {
		t.Run(strings.ReplaceAll(line, "\n", "-"), func(t *testing.T) {
			p := &promptTerminal{line: line}
			calls, approvals := 0, 0
			p.onRead = func() {
				if len(p.writes) != 2 || p.flushes != 1 || !strings.Contains(p.writes[0], "Privacy:") || !strings.Contains(p.writes[1], "default: decline") {
					t.Fatal("input accepted before reviewed disclosure/flush/prompt")
				}
			}
			c := httpsPromptClient(func(ctx context.Context, target string, confirm func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error) {
				calls++
				r := httpsPromptReview()
				decisionCtx, cancel := context.WithDeadline(ctx, r.ExpiresAt)
				defer cancel()
				approved, err := confirm(decisionCtx, r)
				if err != nil {
					return api.HTTPSCheckResult{}, err
				}
				if approved {
					approvals++
					return api.HTTPSCheckResult{Outcome: "completed", Review: r, RunID: strings.Repeat("b", 32)}, nil
				}
				return api.HTTPSCheckResult{Outcome: "declined", Review: r}, nil
			})
			if err := performHTTPSCheck(context.Background(), c, p, "https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
				t.Fatal(err)
			}
			want := 0
			if line == "check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" {
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

func TestHTTPSPromptFailuresDoNotApproveOrRetry(t *testing.T) {
	for _, mode := range []string{"review-write", "prompt-write", "ack-write", "flush", "eof", "expired", "canceled-after-input"} {
		t.Run(mode, func(t *testing.T) {
			p := &promptTerminal{line: "check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}
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
			c := httpsPromptClient(func(ctx context.Context, _ string, confirm func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error) {
				calls++
				r := httpsPromptReview()
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
				return api.HTTPSCheckResult{}, err
			})
			err := performHTTPSCheck(context.Background(), c, p, "https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if err == nil || approvals != 0 || calls != 1 || !strings.Contains(err.Error(), "no approval was submitted") || strings.Contains(err.Error(), "private") {
				t.Fatalf("%v calls=%d approvals=%d", err, calls, approvals)
			}
		})
	}
}

func TestHTTPSPromptInterruptedAfterApprovalIsUnknown(t *testing.T) {
	p := &promptTerminal{line: "check https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}
	calls := 0
	c := httpsPromptClient(func(ctx context.Context, _ string, f func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error) {
		calls++
		r := httpsPromptReview()
		dc, cancel := context.WithDeadline(ctx, r.ExpiresAt)
		defer cancel()
		ok, err := f(dc, r)
		if !ok || err != nil {
			t.Fatal("missing approval", err)
		}
		return api.HTTPSCheckResult{}, errors.New("private session detail")
	})
	err := performHTTPSCheck(context.Background(), c, p, "https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "outcome may be unknown") || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}

func TestHTTPSCommandRejectsUnattendedFlagsBeforeStateAccess(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	dir := filepath.Join(t.TempDir(), "must-not-exist")
	for _, args := range [][]string{{}, {"--yes", "https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"--approve", "https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"--json", "https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"8.8.8.8"}, {"example.test"}, {"https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "extra"}, {"https-selection.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}} {
		full := append([]string{"https-check", "--state-dir", dir}, args...)
		if err := run(context.Background(), full, out, out); err == nil {
			t.Fatal("invalid/nonterminal command accepted", args)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("failed check mutated state", err)
	}
	if err := run(context.Background(), []string{"https-check", "--help"}, out, out); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSPresentationPreservesDisclosureAndStatus(t *testing.T) {
	r := httpsPromptReview()
	r.Policy = api.HTTPSPlanPolicy{MinTLSVersion: "TLS 1.2", MaxTLSVersion: "TLS 1.3", TrustStore: "system", VerifyServerIdentity: true, ALPN: "http/1.1", HTTPVersion: "HTTP/1.1", FreshConnection: true}
	r.Budget = api.HTTPSPlanBudget{MaxConnections: 1, MaxRequests: 1, MaxRequestBytes: 177, MaxResponseHeaderBytes: 16384, MaxTransportReadBytes: 131072, MaxTransportWriteBytes: 32768, MaxTransportReadCalls: 512, MaxTransportWriteCalls: 64, ConnectTimeoutMS: 2000, TLSHandshakeTimeoutMS: 3000, ResponseHeaderTimeoutMS: 2000, TotalTimeoutMS: 8000, MaxConcurrentRuns: 1, MinRunIntervalMS: 60000}
	r.RouteObservedAt = r.CreatedAt.Add(-time.Second)
	r.RouteFreshUntil = r.ExpiresAt
	text := httpsReviewText(r)
	for _, want := range []string{r.SelectionID, r.Settings.Endpoint, r.Settings.ServerName, "HEAD /check", "expected status: 204", `HEAD /check HTTP/1.1\r\nHost: test.example\r\n\r\n`, "TLS 1.2 to TLS 1.3", "trust: system", "verify identity: true", "ALPN: http/1.1", "1 connection, 1 request, 0 retries", "177 request bytes", "16384 response-header bytes", "131072 read bytes / 512 calls", "32768 write bytes / 64 calls", "connect 2000 ms; TLS 3000 ms; response headers 2000 ms; total 8000 ms", "60000 ms", "Proxy: false; name resolution: false; redirects: false; response-body reading: false", r.Privacy[0], r.RouteObservedAt.Format(time.RFC3339Nano), r.RouteFreshUntil.Format(time.RFC3339Nano)} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing disclosure %q", want)
		}
	}
	if strings.Contains(text, "HEAD /check HTTP/1.1\r\n") {
		t.Fatal("request rendered as raw control bytes")
	}
	zero := int64(0)
	result := api.HTTPSCheckResult{Review: r, Outcome: "completed", RunID: "run", Measurement: &api.HTTPSRunMeasurement{StartedAt: r.CreatedAt, CompletedAt: r.CreatedAt.Add(time.Millisecond), Stage: "request", Request: "accepted", Exchange: "response-received", StatusCode: 503, ResponseTimeNanoseconds: &zero}}
	text = httpsResultText(result)
	for _, want := range []string{"Execution outcome: completed", "Received HTTP status: 503; expected: 204; matches expectation: false", "0s", "Historical evidence only", "not pure network RTT"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost evidence %q", want)
		}
	}
	result.Measurement.StatusCode = 204
	if !strings.Contains(httpsResultText(result), "matches expectation: true") {
		t.Fatal("expected status lost")
	}
}
