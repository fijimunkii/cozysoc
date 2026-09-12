package resolverudp

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

type fakeSocket struct {
	packet                                []byte
	port                                  uint16
	sends, reads, verifies, closes        int
	sendErr, verifyErr, readErr, closeErr error
	attempt                               bool
	receiveFunc                           func() (datagram, error)
	verifyFunc                            func() error
}

func (f *fakeSocket) verify(context.Context) error {
	f.verifies++
	if f.verifyFunc != nil {
		return f.verifyFunc()
	}
	return f.verifyErr
}
func (f *fakeSocket) send(_ context.Context, b []byte) (bool, error) {
	f.sends++
	f.packet = append([]byte(nil), b...)
	return f.attempt, f.sendErr
}
func (f *fakeSocket) receive(context.Context) (datagram, error) {
	f.reads++
	if f.receiveFunc != nil {
		return f.receiveFunc()
	}
	return datagram{}, f.readErr
}
func (f *fakeSocket) close() error { f.closes++; return f.closeErr }
func fixture(t testing.TB) (*Sender, *fakeSocket, resolverrun.Request, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	p, e := resolverplan.New(resolverplan.Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, resolverplan.Configuration{Selection: nq.ResolverSelection{ID: "dns1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectNXDOMAIN}, Endpoint: netip.MustParseAddrPort("192.0.2.53:53"), Name: "test.example.", DestinationScope: resolverplan.EnrolledPrefix}, now)
	if e != nil {
		t.Fatal(e)
	}
	r := resolverrun.Request{Selection: resolverrun.Selection{Plan: p, RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}, MeasurementID: strings.Repeat("a", 32)}
	f := &fakeSocket{attempt: true, readErr: errWouldBlock}
	s := &Sender{now: func() time.Time { return now }, random: strings.NewReader("\x12\x34\x56\x78"), wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, open: func(_ context.Context, _ resolverrun.Request, port uint16) (socket, error) {
		f.port = port
		return f, nil
	}}
	s.lookup = func(context.Context, resolverrun.Request) (resolverrun.Selection, error) {
		d := r.Selection.Plan.Disclosure()
		plan, e := resolverplan.New(d.Binding, d.Configuration, now)
		return resolverrun.Selection{Plan: plan, RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}, e
	}
	return s, f, r, &now
}
func deadline(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func reply(f *fakeSocket, r resolverrun.Request) datagram {
	b := append([]byte(nil), f.packet...)
	binary.BigEndian.PutUint16(b[2:4], 0x8103)
	d := r.Selection.Plan.Disclosure()
	return datagram{data: b, peer: d.Configuration.Endpoint, local: d.Binding.Source, index: d.Binding.Observer.InterfaceIndex}
}
func TestOneSendMatchedErrorAndTimeout(t *testing.T) {
	for _, answer := range []bool{false, true} {
		s, f, r, now := fixture(t)
		if answer {
			f.receiveFunc = func() (datagram, error) { *now = now.Add(time.Millisecond); return reply(f, r), nil }
		}
		m, e := s.ExecuteResolver(deadline(t), r)
		if e != nil || f.sends != 1 || f.closes != 1 || f.port < 49152 || m.Request != nq.DNSRequestAccepted {
			t.Fatalf("%+v %v", m, e)
		}
		if answer {
			if m.Exchange != nq.DNSResponseReceived || m.Reply.RCode != 3 || m.ResponseTime == nil || *m.ResponseTime != time.Millisecond {
				t.Fatal("NXDOMAIN lost")
			}
		} else if m.Exchange != nq.DNSTimeout || m.CompletedAt.Sub(m.StartedAt) < 2*time.Second || m.Reply != nil {
			t.Fatal("invalid timeout")
		}
	}
}
func TestForeignMalformedAndBudgetExhaustion(t *testing.T) {
	for _, mode := range []string{"id", "peer", "local", "interface", "truncated", "oversized", "question", "read-calls"} {
		t.Run(mode, func(t *testing.T) {
			s, f, r, _ := fixture(t)
			if mode == "read-calls" {
				s.wait = func(context.Context, time.Duration) error { return nil }
			} else {
				f.receiveFunc = func() (datagram, error) {
					d := reply(f, r)
					switch mode {
					case "id":
						d.data[0] ^= 1
					case "peer":
						d.peer = netip.MustParseAddrPort("192.0.2.54:53")
					case "local":
						d.local = netip.MustParseAddr("192.0.2.11")
					case "interface":
						d.index++
					case "truncated":
						d.truncated = true
					case "oversized":
						d.data = make([]byte, 513)
					case "question":
						d.data[13] = 'x'
					}
					return d, nil
				}
			}
			m, e := s.ExecuteResolver(deadline(t), r)
			if !errors.Is(e, ErrReceiveBudget) || m.Exchange != nq.DNSIncomplete || m.Reply != nil || f.sends != 1 || f.closes != 1 {
				t.Fatalf("%+v %v", m, e)
			}
			want := 16
			if mode == "read-calls" {
				want = 512
			}
			if f.reads != want {
				t.Fatal("receive budget not enforced", f.reads)
			}
		})
	}
}
func TestNoRetryOnUncertainSendOrBindingFailure(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		s, f, r, _ := fixture(t)
		f.attempt = attempted
		f.sendErr = errors.New("private kernel failure")
		m, e := s.ExecuteResolver(deadline(t), r)
		if e != ErrUnavailable || f.sends != 1 || f.reads != 0 || f.closes != 1 || m.Reply != nil {
			t.Fatalf("%+v %v", m, e)
		}
		want := nq.DNSRequestNotSent
		if attempted {
			want = nq.DNSRequestUncertain
		}
		if m.Request != want {
			t.Fatal("invented acceptance")
		}
	}
	s, f, r, _ := fixture(t)
	f.verifyErr = ErrBinding
	m, e := s.ExecuteResolver(deadline(t), r)
	if e != ErrBinding || f.sends != 0 || m.Request != nq.DNSRequestNotSent || f.closes != 1 {
		t.Fatal("binding failure sent")
	}
}
func TestRouteChangesBeforeSendAndAfterResponse(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		s, f, r, now := fixture(t)
		calls := 0
		lookup := s.lookup
		s.lookup = func(ctx context.Context, r resolverrun.Request) (resolverrun.Selection, error) {
			calls++
			if calls == failAt {
				return resolverrun.Selection{}, ErrBinding
			}
			return lookup(ctx, r)
		}
		f.receiveFunc = func() (datagram, error) { *now = now.Add(time.Millisecond); return reply(f, r), nil }
		m, e := s.ExecuteResolver(deadline(t), r)
		if e != ErrBinding || m.Exchange != nq.DNSIncomplete || m.Reply != nil {
			t.Fatalf("%+v %v", m, e)
		}
		if failAt < 3 && f.sends != 0 {
			t.Fatal("sent after route mismatch")
		}
		if failAt == 3 && m.Request != nq.DNSRequestAccepted {
			t.Fatal("lost accepted request")
		}
	}
}
func TestDeadlinesCancellationAndClockRollback(t *testing.T) {
	for _, mode := range []string{"cancel", "rollback", "forward", "late", "budget-at-deadline"} {
		t.Run(mode, func(t *testing.T) {
			s, f, r, now := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			f.receiveFunc = func() (datagram, error) {
				switch mode {
				case "cancel":
					cancel()
				case "rollback":
					*now = now.Add(-time.Second)
				case "forward":
					*now = now.Add(time.Hour)
				case "late":
					*now = now.Add(2 * time.Second)
				case "budget-at-deadline":
					if f.reads == 16 {
						*now = now.Add(2 * time.Second)
					}
					return datagram{}, nil
				}
				return reply(f, r), nil
			}
			m, e := s.ExecuteResolver(ctx, r)
			if mode == "late" {
				if e != nil || m.Exchange != nq.DNSTimeout {
					t.Fatalf("late reply counted: %+v %v", m, e)
				}
			} else if e == nil || m.Reply != nil || m.Exchange != nq.DNSIncomplete {
				t.Fatalf("invalid timing accepted %+v %v", m, e)
			}
			if f.sends != 1 || f.closes != 1 {
				t.Fatal("lost cleanup or retried")
			}
		})
	}
}
func TestCleanupFailurePreservesMatchedEvidence(t *testing.T) {
	s, f, r, _ := fixture(t)
	f.receiveFunc = func() (datagram, error) { return reply(f, r), nil }
	f.closeErr = errors.New("private close error")
	m, e := s.ExecuteResolver(deadline(t), r)
	if e != ErrUnavailable || m.Reply == nil || m.Reply.RCode != 3 || m.ResponseTime == nil || *m.ResponseTime != 0 {
		t.Fatalf("%+v %v", m, e)
	}
}
func TestUnavailableEntropyAndMissingDeadline(t *testing.T) {
	s, f, r, _ := fixture(t)
	if _, e := s.ExecuteResolver(context.Background(), r); e != ErrBinding {
		t.Fatal("unbounded context accepted")
	}
	s.random = strings.NewReader("")
	if _, e := s.ExecuteResolver(deadline(t), r); e != ErrUnavailable || f.sends != 0 || f.port != 0 {
		t.Fatal("entropy failure opened socket")
	}
	s.busy.Store(true)
	if _, e := s.ExecuteResolver(deadline(t), r); e != ErrBusy {
		t.Fatal("concurrent run accepted")
	}
}

func TestRealClockResponseEvidenceRemainsConsistent(t *testing.T) {
	for n := 0; n < 1000; n++ {
		s, f, r, _ := fixture(t)
		s.now = time.Now
		d := r.Selection.Plan.Disclosure()
		now := time.Now()
		p, e := resolverplan.New(d.Binding, d.Configuration, now)
		if e != nil {
			t.Fatal(e)
		}
		r.Selection = resolverrun.Selection{Plan: p, RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}
		s.lookup = func(context.Context, resolverrun.Request) (resolverrun.Selection, error) {
			at := time.Now()
			p, e := resolverplan.New(d.Binding, d.Configuration, at)
			return resolverrun.Selection{Plan: p, RouteObservedAt: at, RouteFreshUntil: at.Add(30 * time.Second)}, e
		}
		f.receiveFunc = func() (datagram, error) {
			dg := reply(f, r)
			binary.BigEndian.PutUint16(dg.data[2:], 0x8180)
			binary.BigEndian.PutUint16(dg.data[6:], 1)
			dg.data = append(dg.data, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 1)
			return dg, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		m, e := s.ExecuteResolver(ctx, r)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if e := nq.ValidateResolverSnapshot(nq.ResolverSnapshot{Observer: m.Observer, Selections: []nq.ResolverSelection{m.Selection}, AsOf: time.Now(), WindowStart: m.StartedAt, Freshness: time.Second, Measurements: []nq.ResolverMeasurement{m}}); e != nil {
			t.Fatalf("response timing inconsistent: elapsed=%s response=%s: %v", m.CompletedAt.Sub(m.StartedAt), *m.ResponseTime, e)
		}
	}
}
