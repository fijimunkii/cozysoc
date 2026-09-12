package httpstcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsexchange"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func TestMeasurementLatencyIncludesConnectButExcludesPostResponseReview(t *testing.T) {
	selected := fixture(t)
	checks := 0
	c := NewCandidate()
	c.inspect = inspectorFunc(func(ctx context.Context, e httpsroute.Enrollment, p httpsplan.Configuration) (httpsroute.Selection, error) {
		checks++
		if checks == 3 {
			time.Sleep(25 * time.Millisecond)
		}
		return freshInspector().Inspect(ctx, e, p)
	})
	c.open = func(context.Context, httpsroute.Selection) (net.Conn, error) {
		time.Sleep(10 * time.Millisecond)
		return &stubConn{}, nil
	}
	var received time.Time
	c.exchange = func(context.Context, httpsplan.Plan, net.Conn) (httpsexchange.Result, error) {
		received = time.Now().Round(0).UTC()
		return httpsexchange.Result{Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, StatusCode: 503, ResponseReceivedAt: received}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	id := strings.Repeat("a", 32)
	m, err := c.ExecuteHTTPS(ctx, Request{MeasurementID: id, Selection: selected})
	if err != nil || m.ID != id || m.Selection != selected.Plan.Disclosure().Configuration.Selection || m.Observer != selected.Plan.Disclosure().Binding.Observer || m.StatusCode != 503 || m.ResponseTime == nil {
		t.Fatal(m, err)
	}
	if *m.ResponseTime != received.Sub(m.StartedAt) || *m.ResponseTime < 10*time.Millisecond || m.CompletedAt.Sub(received) < 25*time.Millisecond {
		t.Fatal("latency omitted connect or included final revalidation", m)
	}
	raw := fmt.Sprintf("%+v", m)
	if strings.Contains(raw, "test.example") || strings.Contains(raw, "198.51.100.20") || strings.Contains(raw, "/check") {
		t.Fatal("private data entered normalized evidence")
	}
}
func TestMeasurementFailuresRemainStageSpecific(t *testing.T) {
	for _, mode := range []string{"connect", "tls", "partial-request", "timeout", "protocol", "changed-binding"} {
		t.Run(mode, func(t *testing.T) {
			c := NewCandidate()
			c.inspect = freshInspector()
			c.open = func(context.Context, httpsroute.Selection) (net.Conn, error) {
				if mode == "connect" {
					return nil, ErrUnavailable
				}
				return &stubConn{}, nil
			}
			c.exchange = func(context.Context, httpsplan.Plan, net.Conn) (httpsexchange.Result, error) {
				r := httpsexchange.Result{Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted}
				switch mode {
				case "tls":
					r.Stage = nq.HTTPSTLS
					r.Request = nq.HTTPSRequestNotSent
					r.Exchange = nq.HTTPSTLSError
					return r, httpsexchange.ErrTLS
				case "partial-request":
					r.Request = nq.HTTPSRequestUncertain
					r.Exchange = nq.HTTPSTransportError
					return r, httpsexchange.ErrTransport
				case "timeout":
					r.Exchange = nq.HTTPSTimeout
					return r, context.DeadlineExceeded
				case "protocol":
					r.Exchange = nq.HTTPSProtocolError
					return r, httpsexchange.ErrProtocol
				default:
					r.Exchange = nq.HTTPSResponseReceived
					r.StatusCode = 204
					r.ResponseReceivedAt = time.Now().UTC()
					c.inspect = inspectorFunc(func(context.Context, httpsroute.Enrollment, httpsplan.Configuration) (httpsroute.Selection, error) {
						return httpsroute.Selection{}, httpsroute.ErrMismatch
					})
					return r, nil
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			m, err := c.ExecuteHTTPS(ctx, Request{MeasurementID: strings.Repeat("a", 32), Selection: fixture(t)})
			if err == nil || m.ID == "" || m.StatusCode != 0 || m.ResponseTime != nil || m.CompletedAt.Before(m.StartedAt) {
				t.Fatal("lost failure evidence", m, err)
			}
			if mode == "connect" && (m.Stage != nq.HTTPSConnect || m.Exchange != nq.HTTPSConnectError) {
				t.Fatal("wrong connect attribution")
			}
			if mode == "changed-binding" && m.Exchange != nq.HTTPSIncomplete {
				t.Fatal("attributed changed route")
			}
		})
	}
}
func TestInvalidMeasurementIdentityDoesNotEnterCandidate(t *testing.T) {
	c := NewCandidate()
	entered := false
	c.inspect = inspectorFunc(func(context.Context, httpsroute.Enrollment, httpsplan.Configuration) (httpsroute.Selection, error) {
		entered = true
		return httpsroute.Selection{}, ErrUnavailable
	})
	for _, id := range []string{"", "raw.private.example", strings.Repeat("g", 32), strings.Repeat("a", 33)} {
		if m, err := c.ExecuteHTTPS(context.Background(), Request{MeasurementID: id, Selection: fixture(t)}); !errors.Is(err, ErrBinding) || m.ID != "" || entered {
			t.Fatal("invalid identity reached route")
		}
	}
}
func TestResponseTimingCannotBeInvented(t *testing.T) {
	for _, mode := range []string{"missing", "before-connect", "future"} {
		t.Run(mode, func(t *testing.T) {
			c := NewCandidate()
			c.inspect = freshInspector()
			c.open = func(context.Context, httpsroute.Selection) (net.Conn, error) { return &stubConn{}, nil }
			c.exchange = func(context.Context, httpsplan.Plan, net.Conn) (httpsexchange.Result, error) {
				at := time.Time{}
				if mode == "before-connect" {
					at = time.Now().Add(-time.Hour)
				}
				if mode == "future" {
					at = time.Now().Add(time.Hour)
				}
				return httpsexchange.Result{Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, StatusCode: 204, ResponseReceivedAt: at}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if m, err := c.ExecuteHTTPS(ctx, Request{MeasurementID: strings.Repeat("a", 32), Selection: fixture(t)}); err == nil || m.ID != "" {
				t.Fatal("invented timing", m, err)
			}
		})
	}
}
func TestOriginalRouteExpiryCapsConnectionDeadline(t *testing.T) {
	s := fixture(t)
	s.RouteObservedAt = time.Now().Add(-29 * time.Second)
	s.RouteFreshUntil = s.RouteObservedAt.Add(30 * time.Second)
	c := NewCandidate()
	c.inspect = freshInspector()
	c.open = func(ctx context.Context, _ httpsroute.Selection) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(s.RouteFreshUntil) {
			t.Fatal("fresh route extended original deadline")
		}
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Execute(ctx, s); err != ErrUnavailable {
		t.Fatal(err)
	}
}
