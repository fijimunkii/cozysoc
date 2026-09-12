// Package httpstcp connects a pinned HTTPS selection using verified native socket
// binding. Only trusted run control may invoke it after audited one-shot consent.
// No product endpoint or approval mechanism invokes this candidate yet.
package httpstcp

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsexchange"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var (
	ErrUnsupported = errors.New("native HTTPS TCP binding unsupported")
	ErrUnavailable = errors.New("native HTTPS TCP unavailable")
	ErrBinding     = errors.New("HTTPS route or socket binding changed")
	ErrPermission  = errors.New("HTTPS TCP binding requires permission")
	ErrBusy        = errors.New("HTTPS TCP candidate is busy")
)

type Candidate struct {
	busy     atomic.Bool
	inspect  httpsroute.Inspector
	open     func(context.Context, httpsroute.Selection) (net.Conn, error)
	exchange func(context.Context, httpsplan.Plan, net.Conn) (httpsexchange.Result, error)
}

// NewCandidate performs no I/O. Construct one per controller; durable admission,
// cooldown, run identity and consent remain responsibilities of future run control.
func NewCandidate() *Candidate {
	return &Candidate{inspect: httpsroute.NewInspector(), open: openSystem, exchange: httpsexchange.Exchange}
}
func safeError(err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrBinding, ErrPermission, ErrUnsupported, ErrBusy, httpsexchange.ErrBudget, httpsexchange.ErrTLS, httpsexchange.ErrProtocol, httpsexchange.ErrTransport, httpsexchange.ErrUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}
func (c *Candidate) verifyRoute(ctx context.Context, s httpsroute.Selection) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d := s.Plan.Disclosure()
	start := time.Now()
	fresh, err := c.inspect.Inspect(ctx, httpsroute.Enrollment{Observer: d.Binding.Observer, Prefixes: d.Binding.Prefixes}, httpsplan.Configuration(d.Configuration))
	if err != nil {
		switch {
		case errors.Is(err, httpsroute.ErrUnsupported):
			return ErrUnsupported
		case errors.Is(err, httpsroute.ErrPermission):
			return ErrPermission
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return err
		default:
			return ErrBinding
		}
	}
	now := time.Now()
	if !s.Plan.SameSelection(fresh.Plan) || !fresh.Plan.Current(now) || fresh.RouteObservedAt.Before(start) || fresh.RouteObservedAt.After(now) || !fresh.RouteFreshUntil.Equal(fresh.RouteObservedAt.Add(httpsplan.ReviewLifetime)) || !now.Before(fresh.RouteFreshUntil) || !s.Plan.Current(now) {
		return ErrBinding
	}
	return ctx.Err()
}

// Execute retains the caller's original absolute deadline across route lookup,
// one TCP connect and TLS/HTTP. A returned response is not a reachability guarantee.
func (c *Candidate) Execute(ctx context.Context, s httpsroute.Selection) (r httpsexchange.Result, err error) {
	if c == nil || c.inspect == nil || c.open == nil || c.exchange == nil {
		return r, ErrUnavailable
	}
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if _, ok := ctx.Deadline(); !ok {
		return r, ErrBinding
	}
	if !c.busy.CompareAndSwap(false, true) {
		return r, ErrBusy
	}
	defer c.busy.Store(false)
	start := time.Now()
	d := s.Plan.Disclosure()
	if !s.Plan.Current(start) || s.RouteObservedAt.IsZero() || s.RouteObservedAt.After(d.CreatedAt) || !s.RouteFreshUntil.Equal(s.RouteObservedAt.Add(httpsplan.ReviewLifetime)) || !start.Before(s.RouteFreshUntil) {
		return r, ErrBinding
	}
	deadline := start.Add(d.Budget.TotalTimeout)
	if d.ExpiresAt.Before(deadline) {
		deadline = d.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := c.verifyRoute(ctx, s); err != nil {
		return r, safeError(err)
	}
	r.Stage = nq.HTTPSConnect
	r.Request = nq.HTTPSRequestNotSent
	r.Exchange = nq.HTTPSIncomplete
	connectCtx, stopConnect := context.WithTimeout(ctx, d.Budget.ConnectTimeout)
	conn, err := c.open(connectCtx, s)
	stopConnect()
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		err = safeError(err)
		r.Exchange = nq.HTTPSConnectError
		if errors.Is(err, context.Canceled) || errors.Is(err, ErrBinding) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrPermission) {
			r.Exchange = nq.HTTPSIncomplete
		}
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
			r.Exchange = nq.HTTPSTimeout
			err = context.DeadlineExceeded
		}
		return r, err
	}
	if conn == nil {
		return r, ErrUnavailable
	}
	defer conn.Close()
	if err := c.verifyRoute(ctx, s); err != nil {
		return r, safeError(err)
	}
	guarded := &routeConn{Conn: conn, check: func() error { return c.verifyRoute(ctx, s) }}
	r, err = c.exchange(ctx, s.Plan, guarded)
	if guarded.failure != nil {
		r.Exchange = nq.HTTPSIncomplete
		r.StatusCode = 0
		return r, safeError(guarded.failure)
	}
	if err != nil {
		return r, safeError(err)
	}
	// Do not attribute a response to an enrollment that changed during receipt.
	if err := c.verifyRoute(ctx, s); err != nil {
		r.Exchange = nq.HTTPSIncomplete
		r.StatusCode = 0
		return r, safeError(err)
	}
	return r, nil
}

// Synchronous TLS writes recheck the unscoped OS route. The native connection's
// own Write additionally verifies the bound interface, source and peer socket.
type routeConn struct {
	net.Conn
	check   func() error
	failure error
}

func (c *routeConn) Write(p []byte) (int, error) {
	if c.failure != nil {
		return 0, c.failure
	}
	if err := c.check(); err != nil {
		c.failure = err
		return 0, err
	}
	n, err := c.Conn.Write(p)
	if errors.Is(err, ErrBinding) || errors.Is(err, ErrPermission) {
		c.failure = err
	}
	return n, err
}
