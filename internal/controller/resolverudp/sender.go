// Package resolverudp implements a bounded DNS/UDP candidate. Only trusted run
// control may invoke it after audited one-shot admission. No product endpoint,
// configuration decoder, scheduler, fallback or consent mechanism lives here.
package resolverudp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"sync/atomic"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverwire"
)

var (
	ErrUnsupported   = errors.New("resolver UDP sender is unsupported")
	ErrUnavailable   = errors.New("resolver UDP transport is unavailable")
	ErrPermission    = errors.New("resolver UDP transport requires permission")
	ErrBinding       = errors.New("resolver UDP route or socket binding changed")
	ErrReceiveBudget = errors.New("resolver UDP receive budget exhausted")
	ErrClock         = errors.New("resolver UDP clock is invalid")
	ErrBusy          = errors.New("resolver UDP sender is busy")
	errWouldBlock    = errors.New("resolver UDP receive would block")
)

const maxControlBytes = 256

var measurementID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type datagram struct {
	data      []byte
	peer      netip.AddrPort
	local     netip.Addr
	index     int
	truncated bool
}
type socket interface {
	verify(context.Context) error
	send(context.Context, []byte) (bool, error) // bool means a send syscall was attempted.
	receive(context.Context) (datagram, error)
	close() error
}
type Sender struct {
	busy   atomic.Bool
	lookup func(context.Context, resolverrun.Request) (resolverrun.Selection, error)
	open   func(context.Context, resolverrun.Request, uint16) (socket, error)
	now    func() time.Time
	wait   func(context.Context, time.Duration) error
	random io.Reader
}

var _ resolverrun.Executor = (*Sender)(nil)

// NewCandidate creates no socket and sends nothing. Construct one per controller.
func NewCandidate() *Sender {
	lookup, open := platform()
	return &Sender{lookup: lookup, open: open, now: time.Now, wait: waitContext, random: rand.Reader}
}
func validTime(t time.Time) bool { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }
func contextError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Round(0).Before(deadline.Round(0)) {
		return context.DeadlineExceeded
	}
	return nil
}
func waitContext(ctx context.Context, d time.Duration) error {
	if e := contextError(ctx); e != nil {
		return e
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return contextError(ctx)
	}
}
func safeError(err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrUnsupported, ErrPermission, ErrBinding, ErrReceiveBudget, ErrClock, ErrBusy} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}

// ExecuteResolver requires the original consumed-approval context, including its
// absolute deadline. New route evidence and a fresh plan never renew that context.
// Request state reports kernel acceptance, not proof of wire egress or DNS success.
func (s *Sender) ExecuteResolver(ctx context.Context, r resolverrun.Request) (m nq.ResolverMeasurement, err error) {
	if e := contextError(ctx); e != nil {
		return m, e
	}
	if _, ok := ctx.Deadline(); !ok {
		return m, ErrBinding
	}
	if s == nil || s.lookup == nil || s.open == nil || s.now == nil || s.wait == nil || s.random == nil {
		return m, ErrUnavailable
	}
	if !s.busy.CompareAndSwap(false, true) {
		return m, ErrBusy
	}
	defer s.busy.Store(false)
	start := s.now()
	d := r.Selection.Plan.Disclosure()
	if !validTime(start) || !r.Selection.Plan.Current(start) || !measurementID.MatchString(r.MeasurementID) || !validTime(r.Selection.RouteObservedAt) || r.Selection.RouteObservedAt.After(d.CreatedAt) || !r.Selection.RouteFreshUntil.Equal(r.Selection.RouteObservedAt.Add(resolverplan.ReviewLifetime)) || !r.Selection.RouteFreshUntil.After(start) {
		return m, ErrBinding
	}
	ctx, cancel := context.WithTimeout(ctx, min(d.Budget.TotalTimeout, d.ExpiresAt.Sub(start.Round(0))))
	defer cancel()
	last := start
	clock := func() (time.Time, error) {
		now := s.now()
		if !validTime(now) || now.Before(last) || now.Round(0).Before(last.Round(0)) {
			return last, ErrClock
		}
		last = now
		if e := contextError(ctx); e != nil {
			return now, e
		}
		if now.Sub(start) >= d.Budget.TotalTimeout || now.Round(0).Sub(start.Round(0)) >= d.Budget.TotalTimeout || !now.Round(0).Before(d.ExpiresAt) {
			return now, context.DeadlineExceeded
		}
		return now, nil
	}
	m = nq.ResolverMeasurement{ID: r.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start.Round(0).UTC(), Exchange: nq.DNSIncomplete, Request: nq.DNSRequestNotSent}
	defer func() {
		if m.CompletedAt.IsZero() {
			now := s.now()
			if validTime(now) && !now.Before(last) && !now.Round(0).Before(last.Round(0)) {
				last = now
			}
			m.CompletedAt = last.Round(0).UTC()
		}
	}()
	checkRoute := func() error {
		before, e := clock()
		if e != nil {
			return e
		}
		fresh, e := s.lookup(ctx, r)
		if e != nil {
			return safeError(e)
		}
		after, e := clock()
		if e != nil {
			return e
		}
		if !r.Selection.Plan.SameSelection(fresh.Plan) || !fresh.Plan.Current(after) || !validTime(fresh.RouteObservedAt) || fresh.RouteObservedAt.Before(before.Round(0)) || fresh.RouteObservedAt.After(after.Round(0)) || fresh.RouteObservedAt.After(fresh.Plan.Disclosure().CreatedAt) || !fresh.RouteFreshUntil.Equal(fresh.RouteObservedAt.Add(resolverplan.ReviewLifetime)) || !fresh.RouteFreshUntil.After(after) {
			return ErrBinding
		}
		return nil
	}
	if e := checkRoute(); e != nil {
		return m, e
	}
	var entropy [4]byte
	if _, e := io.ReadFull(s.random, entropy[:]); e != nil {
		return m, ErrUnavailable
	}
	query, e := resolverwire.NewQuery(d.Configuration.Endpoint, d.Configuration.Name, d.Configuration.Selection.QueryType, binary.BigEndian.Uint16(entropy[:2]))
	if e != nil {
		return m, ErrBinding
	}
	port := uint16(49152) + (binary.BigEndian.Uint16(entropy[2:]) & 16383)
	if _, e := clock(); e != nil {
		return m, e
	}
	conn, e := s.open(ctx, r, port)
	if e != nil {
		return m, safeError(e)
	}
	if conn == nil {
		return m, ErrUnavailable
	}
	defer func() {
		if e := conn.close(); e != nil && err == nil {
			err = ErrUnavailable
		}
	}()
	if e := checkRoute(); e != nil {
		return m, e
	}
	if e := conn.verify(ctx); e != nil {
		return m, safeError(e)
	}
	sentAt, e := clock()
	if e != nil {
		return m, e
	}
	m.StartedAt = sentAt.Round(0).UTC()
	attempted, sendErr := conn.send(ctx, query.Bytes())
	if attempted {
		m.Request = nq.DNSRequestUncertain
	}
	if attempted && sendErr == nil {
		m.Request = nq.DNSRequestAccepted
	}
	if _, e := clock(); e != nil {
		return m, e
	}
	if sendErr != nil {
		m.Exchange = nq.DNSTransportError
		return m, safeError(sendErr)
	}
	if !attempted {
		return m, ErrUnavailable
	}
	m.Request = nq.DNSRequestAccepted
	until := sentAt.Add(d.Budget.ExchangeTimeout)
	received, reads := 0, 0
	for {
		now, e := clock()
		if e != nil {
			return m, e
		}
		if received >= d.Budget.MaxReceivedDatagrams || reads >= d.Budget.MaxReceiveCalls {
			return m, ErrReceiveBudget
		}
		if !now.Before(until) {
			if e := checkRoute(); e != nil {
				return m, e
			}
			if e := conn.verify(ctx); e != nil {
				return m, safeError(e)
			}
			if _, e := clock(); e != nil {
				return m, e
			}
			m.Exchange = nq.DNSTimeout
			return m, nil
		}
		reads++
		packet, readErr := conn.receive(ctx)
		now, e = clock()
		if e != nil {
			return m, e
		}
		if errors.Is(readErr, errWouldBlock) {
			if e := s.wait(ctx, min(10*time.Millisecond, max(0, until.Sub(now)))); e != nil {
				return m, safeError(e)
			}
			continue
		}
		if readErr != nil {
			m.Exchange = nq.DNSTransportError
			return m, safeError(readErr)
		}
		received++
		if !now.Before(until) {
			continue
		}
		if packet.truncated || packet.local != d.Binding.Source || packet.index != d.Binding.Observer.InterfaceIndex {
			continue
		}
		reply, e := query.MatchResponse(packet.peer, packet.data)
		if e != nil {
			continue
		}
		arrived := now
		if e := checkRoute(); e != nil {
			return m, e
		}
		if e := conn.verify(ctx); e != nil {
			return m, safeError(e)
		}
		if _, e := clock(); e != nil {
			return m, e
		}
		// Store timing in the same clock domain as the evidence timestamps. Darwin
		// wall timestamps can be coarser than the monotonic counter; mixing them
		// can put an RTT outside its own recorded interval by a fraction of a µs.
		duration := arrived.Round(0).Sub(sentAt.Round(0))
		if duration < 0 || duration >= d.Budget.ExchangeTimeout {
			return m, ErrClock
		}
		m.Exchange = nq.DNSResponseReceived
		m.Reply = &reply
		m.ResponseTime = &duration
		m.CompletedAt = arrived.Round(0).UTC()
		return m, nil
	}
}
