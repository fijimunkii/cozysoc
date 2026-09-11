package gatewayicmp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// Synthetic scope, route, clock and packet-free transport. No test pings a host.
func requestAt(now time.Time) Request {
	p, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{
		ScopeID: "scope.fixture", InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24", "fe80::/64"}}, "192.168.50.1", now)
	if err != nil {
		panic(err)
	}
	return Request{Plan: p, Source: netip.MustParseAddr("192.168.50.23")}
}

type fakeSocket struct {
	now                     *time.Time
	r                       Request
	sends                   [][]byte
	times                   []time.Time
	verifies, closes, reads int
	verifyHook              func() error
	sendHook                func() error
	readHook                func() (datagram, error)
	closeErr                error
}

func (s *fakeSocket) verify(context.Context) error {
	s.verifies++
	if s.verifyHook != nil {
		return s.verifyHook()
	}
	return nil
}
func (s *fakeSocket) send(ctx context.Context, p []byte) error {
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > 5*time.Second {
		return errors.New("unbounded send")
	}
	s.sends = append(s.sends, bytes.Clone(p))
	s.times = append(s.times, *s.now)
	if s.sendHook != nil {
		return s.sendHook()
	}
	return nil
}
func (s *fakeSocket) reply() datagram {
	p := bytes.Clone(s.sends[len(s.sends)-1])
	p[0], p[2], p[3] = 0, 0, 0
	binary.BigEndian.PutUint16(p[2:], checksum(p))
	return datagram{data: p, peer: s.r.Plan.Target, local: s.r.Source, index: s.r.Plan.Binding.InterfaceIndex}
}
func (s *fakeSocket) receive(context.Context) (datagram, error) {
	s.reads++
	if s.readHook != nil {
		return s.readHook()
	}
	*s.now = s.now.Add(5 * time.Millisecond)
	return s.reply(), nil
}
func (s *fakeSocket) close() error { s.closes++; return s.closeErr }

func fixture(t *testing.T) (*Sender, Request, *fakeSocket, *time.Time, *int) {
	t.Helper()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	r := requestAt(now)
	conn := &fakeSocket{now: &now, r: r}
	lookups := 0
	s := &Sender{now: func() time.Time { return now }, random: bytes.NewReader(bytes.Repeat([]byte{37}, 34)),
		wait: func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() },
		lookup: func(ctx context.Context, req Request) (routeEvidence, error) {
			lookups++
			if !reflect.DeepEqual(req, r) {
				t.Fatal("changed request")
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("unbounded route check")
			}
			return routeEvidence{name: r.Plan.Binding.InterfaceName, index: r.Plan.Binding.InterfaceIndex, source: r.Source,
				observedAt: now, freshUntil: now.Add(30 * time.Second)}, nil
		}, open: func(ctx context.Context, req Request) (socket, error) {
			if !reflect.DeepEqual(req, r) {
				t.Fatal("changed socket selection")
			}
			return conn, ctx.Err()
		},
	}
	return s, r, conn, &now, &lookups
}

func TestSamplePacesThreeEchoesAndRechecksEverySend(t *testing.T) {
	s, r, conn, _, lookups := fixture(t)
	got, err := s.Measure(context.Background(), r)
	if err != nil || !got.Complete || got.SendCalls != 3 || got.AcceptedRequests != 3 || got.Replies != 3 || got.Timeouts != 0 ||
		got.MeanRTT == nil || *got.MeanRTT != 5*time.Millisecond || got.CompletedAt.Sub(got.StartedAt) != 2005*time.Millisecond {
		t.Fatalf("unexpected sample: %+v %v", got, err)
	}
	if *lookups != 4 || conn.verifies != 3 || conn.closes != 1 {
		t.Fatal("missing per-send verification/cleanup")
	}
	if got.ScopeID != r.Plan.Binding.ScopeID || got.Source != r.Source || got.Target != r.Plan.Target || got.InterfaceIndex != 7 {
		t.Fatal("lost provenance")
	}
	for i, packet := range conn.sends {
		if len(packet) != 40 || packet[0] != 8 || packet[1] != 0 || checksum(packet) != 0 || binary.BigEndian.Uint16(packet[6:]) != uint16(i+1) {
			t.Fatal("invalid/bloated echo")
		}
		if i > 0 && conn.times[i].Sub(conn.times[i-1]) < time.Second {
			t.Fatal("burst")
		}
	}
}

func TestCompletedTimeoutsAreNotMissingOrZeroLatency(t *testing.T) {
	for replies := 0; replies <= 2; replies++ {
		s, r, conn, _, _ := fixture(t)
		conn.readHook = func() (datagram, error) {
			if len(conn.sends) <= replies {
				*conn.now = conn.now.Add(10 * time.Millisecond)
				return conn.reply(), nil
			}
			return datagram{}, errWouldBlock
		}
		got, err := s.Measure(context.Background(), r)
		if err != nil || !got.Complete || got.Replies != replies || got.Timeouts != 3-replies || got.AcceptedRequests != 3 || (got.MeanRTT == nil) != (replies == 0) {
			t.Fatalf("replies=%d sample=%+v %v", replies, got, err)
		}
	}
}

func TestRejectsInvalidSelectionBeforeRouteOrSocket(t *testing.T) {
	for name, edit := range map[string]func(*Request){
		"public":               func(r *Request) { r.Plan.Target = netip.MustParseAddr("8.8.8.8") },
		"outside scope":        func(r *Request) { r.Plan.Target = netip.MustParseAddr("10.0.0.1") },
		"broadcast":            func(r *Request) { r.Plan.Target = netip.MustParseAddr("192.168.50.255") },
		"self":                 func(r *Request) { r.Source = r.Plan.Target },
		"source outside scope": func(r *Request) { r.Source = netip.MustParseAddr("10.0.0.1") },
		"mapped":               func(r *Request) { r.Source = netip.MustParseAddr("::ffff:192.168.50.23") },
		"budget":               func(r *Request) { r.Plan.Budget.MaxAttempts++ },
		"payload":              func(r *Request) { r.Plan.Budget.PayloadBytes++ },
		"scope":                func(r *Request) { r.Plan.Binding.ScopeID = "<script>" },
		"index":                func(r *Request) { r.Plan.Binding.InterfaceIndex = 0 },
		"empty":                func(r *Request) { r.Plan.CreatedAt = time.Time{} },
		"future":               func(r *Request) { r.Plan.CreatedAt = r.Plan.CreatedAt.Add(time.Second) },
		"expiry":               func(r *Request) { r.Plan.ReviewExpiresAt = r.Plan.CreatedAt },
		"inflated expiry":      func(r *Request) { r.Plan.ReviewExpiresAt = r.Plan.ReviewExpiresAt.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			s, r, conn, _, lookups := fixture(t)
			edit(&r)
			got, err := s.Measure(context.Background(), r)
			if err == nil || *lookups != 0 || conn.closes != 0 || !reflect.DeepEqual(got, Sample{}) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestRouteAndSocketChangesStopBeforeAnotherSend(t *testing.T) {
	for _, phase := range []int{1, 2, 3, 4} {
		for _, kind := range []string{"error", "source", "index", "name", "old", "future", "inflated"} {
			t.Run(kind+string(rune('0'+phase)), func(t *testing.T) {
				s, r, conn, now, _ := fixture(t)
				base, count := s.lookup, 0
				s.lookup = func(ctx context.Context, request Request) (routeEvidence, error) {
					count++
					e, err := base(ctx, request)
					if count == phase {
						switch kind {
						case "error":
							return routeEvidence{}, errors.New("private-route-details")
						case "source":
							e.source = netip.MustParseAddr("192.168.50.24")
						case "index":
							e.index++
						case "name":
							e.name = "utun0"
						case "old":
							e.observedAt = now.Add(-time.Second)
						case "future":
							e.observedAt = now.Add(time.Second)
						case "inflated":
							e.freshUntil = now.Add(time.Hour)
						}
					}
					return e, err
				}
				got, err := s.Measure(context.Background(), r)
				if err == nil || strings.Contains(err.Error(), "private") || got.Complete || got.MeanRTT != nil || len(conn.sends) != max(0, phase-2) {
					t.Fatalf("%+v %v", got, err)
				}
				if phase > 1 && conn.closes != 1 {
					t.Fatal("socket leaked")
				}
			})
		}
	}
	s, r, conn, _, _ := fixture(t)
	conn.verifyHook = func() error {
		if conn.verifies == 2 {
			return ErrBinding
		}
		return nil
	}
	got, err := s.Measure(context.Background(), r)
	if !errors.Is(err, ErrBinding) || got.SendCalls != 1 || got.Complete || conn.closes != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUnrelatedMalformedAndDuplicateRepliesStayBounded(t *testing.T) {
	s, r, conn, _, _ := fixture(t)
	conn.readHook = func() (datagram, error) { d := conn.reply(); d.index++; return d, nil }
	got, err := s.Measure(context.Background(), r)
	if !errors.Is(err, ErrReceiveBudget) || got.Complete || got.Replies != 0 || got.Timeouts != 0 || got.SendCalls != 1 || conn.reads != 16 || conn.closes != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	// One duplicate from a previous sequence is ignored, not counted twice.
	s, r, conn, _, _ = fixture(t)
	var prior datagram
	injected := false
	conn.readHook = func() (datagram, error) {
		if len(conn.sends) == 2 && !injected {
			injected = true
			return prior, nil
		}
		d := conn.reply()
		prior = d
		return d, nil
	}
	got, err = s.Measure(context.Background(), r)
	if err != nil || !got.Complete || got.Replies != 3 || conn.reads != 4 {
		t.Fatalf("duplicate counted: %+v %v", got, err)
	}
	// Even an injected broken wait and an always-empty transport cannot spin forever.
	s, r, conn, _, _ = fixture(t)
	s.wait = func(context.Context, time.Duration) error { return nil }
	conn.readHook = func() (datagram, error) { return datagram{}, errWouldBlock }
	if got, err := s.Measure(context.Background(), r); !errors.Is(err, ErrReceiveBudget) || got.Complete || conn.reads != maxReceiveCalls {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSendReadAndCloseFailuresNeverBecomeLossOrSuccess(t *testing.T) {
	for _, where := range []string{"open", "send", "read", "close"} {
		s, r, conn, _, _ := fixture(t)
		secret := errors.New("private-kernel-diagnostics")
		switch where {
		case "open":
			s.open = func(context.Context, Request) (socket, error) { return nil, secret }
		case "send":
			conn.sendHook = func() error { return secret }
		case "read":
			conn.readHook = func() (datagram, error) { return datagram{}, secret }
		case "close":
			conn.closeErr = secret
		}
		got, err := s.Measure(context.Background(), r)
		if !errors.Is(err, ErrUnavailable) || got.Complete || got.MeanRTT != nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("%s %+v %v", where, got, err)
		}
		if where == "send" && (got.SendCalls != 1 || got.AcceptedRequests != 0 || len(conn.sends) != 1) {
			t.Fatal("uncertain send retried or counted as accepted")
		}
		if where != "open" && conn.closes != 1 {
			t.Fatal("socket leaked")
		}
	}
}

func TestCancellationExpiryAndRollbackDoNotAdmitLateReplies(t *testing.T) {
	for _, mode := range []string{"pre-cancel", "during-read", "during-route", "after-verify", "rollback", "total-timeout", "reply-at-deadline"} {
		t.Run(mode, func(t *testing.T) {
			s, r, conn, now, lookups := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "pre-cancel":
				cancel()
			case "during-route":
				base := s.lookup
				s.lookup = func(ctx context.Context, req Request) (routeEvidence, error) {
					e, err := base(ctx, req)
					cancel()
					return e, err
				}
			case "after-verify":
				conn.verifyHook = func() error { *now = now.Add(30 * time.Second); return nil }
			default:
				conn.readHook = func() (datagram, error) {
					switch mode {
					case "during-read":
						cancel()
					case "rollback":
						*now = now.Add(-time.Nanosecond)
					case "total-timeout":
						*now = now.Add(5 * time.Second)
					case "reply-at-deadline":
						*now = now.Add(time.Second)
					}
					return conn.reply(), nil
				}
			}
			got, err := s.Measure(ctx, r)
			if mode == "reply-at-deadline" {
				if err != nil || !got.Complete || got.Replies != 0 || got.Timeouts != 3 || got.MeanRTT != nil {
					t.Fatalf("late reply counted: %+v %v", got, err)
				}
				return
			}
			if err == nil || got.Complete || got.MeanRTT != nil {
				t.Fatalf("%+v %v", got, err)
			}
			if mode == "pre-cancel" && (*lookups != 0 || conn.closes != 0) {
				t.Fatal("canceled call inspected OS")
			}
			if mode == "after-verify" && got.SendCalls != 0 {
				t.Fatal("expiry during socket verification still sent")
			}
		})
	}
}

func TestOriginalContextIsPreservedAndSampleCannotOutliveReview(t *testing.T) {
	s, r, conn, now, _ := fixture(t)
	*now = now.Add(29 * time.Second)
	originalOpen := s.open
	s.open = func(ctx context.Context, r Request) (socket, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Fatal("extended review window")
		}
		return originalOpen(ctx, r)
	}
	got, err := s.Measure(context.Background(), r)
	if !errors.Is(err, context.DeadlineExceeded) || got.Complete || got.SendCalls != 1 || conn.closes != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestBusyEntropyAndDefensiveCopies(t *testing.T) {
	s, r, conn, _, _ := fixture(t)
	base := s.lookup
	s.lookup = func(ctx context.Context, req Request) (routeEvidence, error) {
		if _, err := s.Measure(ctx, r); !errors.Is(err, ErrBusy) {
			t.Fatal("concurrent sample allowed")
		}
		e, err := base(ctx, req)
		req.Plan.Binding.Prefixes[0] = "10.0.0.0/8"
		return e, err
	}
	if _, err := s.Measure(context.Background(), r); err != nil || conn.closes != 1 {
		t.Fatal(err)
	}
	if r.Plan.Binding.Prefixes[0] != "192.168.50.0/24" {
		t.Fatal("caller slice mutated")
	}
	s, r, conn, _, _ = fixture(t)
	s.random = strings.NewReader("")
	if got, err := s.Measure(context.Background(), r); !errors.Is(err, ErrUnavailable) || got.SendCalls != 0 || conn.closes != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	var empty Sender
	if _, err := empty.Measure(context.Background(), r); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestSlowSendDoesNotCauseCatchUpBurst(t *testing.T) {
	s, r, conn, now, _ := fixture(t)
	conn.sendHook = func() error { *now = now.Add(800 * time.Millisecond); return nil }
	got, err := s.Measure(context.Background(), r)
	if err != nil || !got.Complete || len(conn.times) != 3 {
		t.Fatalf("slow sends: %+v %v", got, err)
	}
	for i := 1; i < len(conn.times); i++ {
		if conn.times[i].Sub(conn.times[i-1]) < 1800*time.Millisecond {
			t.Fatal("paced from pre-syscall time rather than completion")
		}
	}
}

func TestCancellationDuringPacingPreventsAnotherRouteReadOrSend(t *testing.T) {
	s, r, conn, _, lookups := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.wait = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	got, err := s.Measure(ctx, r)
	if !errors.Is(err, context.Canceled) || got.Complete || got.MeanRTT != nil ||
		got.SendCalls != 1 || got.Replies != 1 || *lookups != 2 || conn.closes != 1 {
		t.Fatalf("pacing cancellation: %+v %v", got, err)
	}
}

func TestReceiveBudgetDoesNotResetForEachEcho(t *testing.T) {
	s, r, conn, _, _ := fixture(t)
	conn.readHook = func() (datagram, error) {
		d := conn.reply()
		if conn.reads != 9 {
			d.peer = netip.MustParseAddr("192.168.50.99")
		}
		return d, nil
	}
	got, err := s.Measure(context.Background(), r)
	if !errors.Is(err, ErrReceiveBudget) || got.Complete || got.MeanRTT != nil ||
		got.SendCalls != 2 || got.Replies != 1 || got.Timeouts != 0 || conn.reads != maxDatagrams {
		t.Fatalf("receive budget reset: %+v %v", got, err)
	}
}
