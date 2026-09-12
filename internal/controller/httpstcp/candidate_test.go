package httpstcp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsexchange"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type inspectorFunc func(context.Context, httpsroute.Enrollment, httpsplan.Configuration) (httpsroute.Selection, error)

func (f inspectorFunc) Inspect(ctx context.Context, e httpsroute.Enrollment, c httpsplan.Configuration) (httpsroute.Selection, error) {
	return f(ctx, e, c)
}
func fixture(t *testing.T) httpsroute.Selection {
	t.Helper()
	now := time.Now()
	p, err := httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: "fixture", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Source: netip.MustParseAddr("192.0.2.10"), Prefixes: []string{"192.0.2.0/24"}}, httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint1", RequestID: "request1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: "test.example", RequestTarget: "/check", DestinationPolicy: httpsplan.ExactEndpoint}, now)
	if err != nil {
		t.Fatal(err)
	}
	return httpsroute.Selection{Plan: p, RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}
}
func freshInspector() inspectorFunc {
	return func(ctx context.Context, e httpsroute.Enrollment, c httpsplan.Configuration) (httpsroute.Selection, error) {
		now := time.Now()
		p, err := httpsplan.New(httpsplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: netip.MustParseAddr("192.0.2.10")}, c, now)
		return httpsroute.Selection{Plan: p, RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}, err
	}
}

type stubConn struct {
	closed  bool
	writes  int
	failure error
}

func (c *stubConn) Read([]byte) (int, error) { return 0, errors.New("unexpected read") }
func (c *stubConn) Write(p []byte) (int, error) {
	c.writes++
	if c.failure != nil {
		return 0, c.failure
	}
	return len(p), nil
}
func (c *stubConn) Close() error                     { c.closed = true; return nil }
func (c *stubConn) LocalAddr() net.Addr              { return nil }
func (c *stubConn) RemoteAddr() net.Addr             { return nil }
func (c *stubConn) SetDeadline(time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }
func TestCandidateRetainsDeadlineAndChecksEveryWrite(t *testing.T) {
	s := fixture(t)
	wire := &stubConn{}
	checks, opens := 0, 0
	deadline := time.Now().Add(time.Second)
	c := &Candidate{inspect: inspectorFunc(func(ctx context.Context, e httpsroute.Enrollment, p httpsplan.Configuration) (httpsroute.Selection, error) {
		checks++
		got, _ := ctx.Deadline()
		if got.After(deadline) {
			t.Fatal("renewed deadline")
		}
		return freshInspector().Inspect(ctx, e, p)
	}), open: func(ctx context.Context, got httpsroute.Selection) (net.Conn, error) {
		opens++
		if !got.Plan.SameSelection(s.Plan) {
			t.Fatal("changed target")
		}
		d, _ := ctx.Deadline()
		if d.After(deadline) {
			t.Fatal("connect renewed deadline")
		}
		return wire, nil
	}, exchange: func(ctx context.Context, p httpsplan.Plan, conn net.Conn) (httpsexchange.Result, error) {
		if _, err := conn.Write([]byte("fixture")); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte("fixture")); err != nil {
			t.Fatal(err)
		}
		return httpsexchange.Result{Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, StatusCode: 204}, nil
	}}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	r, err := c.Execute(ctx, s)
	if err != nil || r.StatusCode != 204 || opens != 1 || checks != 5 || wire.writes != 2 || !wire.closed {
		t.Fatal("lost lifecycle", r, err, checks, opens)
	}
}
func TestChangedRouteOrSocketStopsWritesAndAttribution(t *testing.T) {
	for _, mode := range []string{"before-connect", "after-connect", "before-write", "after-response", "socket-binding"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t)
			wire := &stubConn{}
			checks, opens := 0, 0
			failAt := map[string]int{"before-connect": 1, "after-connect": 2, "before-write": 3, "after-response": 4}[mode]
			if mode == "socket-binding" {
				wire.failure = ErrBinding
			}
			c := &Candidate{inspect: inspectorFunc(func(ctx context.Context, e httpsroute.Enrollment, p httpsplan.Configuration) (httpsroute.Selection, error) {
				checks++
				if checks == failAt {
					return httpsroute.Selection{}, httpsroute.ErrMismatch
				}
				return freshInspector().Inspect(ctx, e, p)
			}), open: func(context.Context, httpsroute.Selection) (net.Conn, error) { opens++; return wire, nil }, exchange: func(ctx context.Context, p httpsplan.Plan, conn net.Conn) (httpsexchange.Result, error) {
				_, err := conn.Write([]byte("fixture"))
				return httpsexchange.Result{Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, StatusCode: 204}, err
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, err := c.Execute(ctx, s)
			if !errors.Is(err, ErrBinding) || r.StatusCode != 0 {
				t.Fatal("attributed changed route", r, err)
			}
			if mode == "before-connect" && opens != 0 {
				t.Fatal("connected before verified route")
			}
			if (mode == "after-connect" || mode == "before-write") && wire.writes != 0 {
				t.Fatal("sent after route changed")
			}
			if opens > 0 && !wire.closed {
				t.Fatal("connection leaked")
			}
		})
	}
}
func TestCandidateRejectsStaleAndUnboundedAdmission(t *testing.T) {
	for _, mode := range []string{"no-deadline", "stale-route", "extended-route", "zero-plan", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t)
			c := NewCandidate()
			called := false
			c.inspect = inspectorFunc(func(context.Context, httpsroute.Enrollment, httpsplan.Configuration) (httpsroute.Selection, error) {
				called = true
				return httpsroute.Selection{}, ErrUnavailable
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			switch mode {
			case "no-deadline":
				ctx = context.Background()
			case "stale-route":
				s.RouteObservedAt = s.RouteObservedAt.Add(-time.Minute)
				s.RouteFreshUntil = s.RouteObservedAt.Add(30 * time.Second)
			case "extended-route":
				s.RouteFreshUntil = s.RouteFreshUntil.Add(time.Second)
			case "zero-plan":
				s.Plan = httpsplan.Plan{}
			case "canceled":
				cancel()
			}
			if _, err := c.Execute(ctx, s); err == nil || called {
				t.Fatal("invalid admission touched route")
			}
		})
	}
}
func TestCandidateBusyAndConnectFailure(t *testing.T) {
	c := NewCandidate()
	c.busy.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Execute(ctx, fixture(t)); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	c.busy.Store(false)
	c.inspect = freshInspector()
	var calls atomic.Int32
	for _, failure := range []error{ErrUnavailable, ErrPermission, context.DeadlineExceeded} {
		c.open = func(context.Context, httpsroute.Selection) (net.Conn, error) { calls.Add(1); return nil, failure }
		r, err := c.Execute(ctx, fixture(t))
		if err != failure || r.Stage != nq.HTTPSConnect || r.Request != nq.HTTPSRequestNotSent {
			t.Fatal(r, err)
		}
		if failure == context.DeadlineExceeded && r.Exchange != nq.HTTPSTimeout {
			t.Fatal("lost connect timeout")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("connect retried")
	}
}
func TestUnsupportedNativeCandidate(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := NewCandidate().Execute(ctx, fixture(t)); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestOverlappingConnectIsRejectedAndCancellationReleasesSlot(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan error, 1)
	c := NewCandidate()
	c.inspect = freshInspector()
	c.open = func(ctx context.Context, _ httpsroute.Selection) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := fixture(t)
	go func() { _, err := c.Execute(ctx, s); done <- err }()
	<-entered
	if _, err := c.Execute(ctx, s); !errors.Is(err, ErrBusy) {
		t.Fatal("overlapping connect admitted", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || c.busy.Load() {
		t.Fatal("cancellation retained slot", err)
	}
}
