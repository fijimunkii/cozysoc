// Package gatewayicmp contains a disconnected candidate for one bounded ICMPv4
// sample. It is not an authorization API and is not installed in the controller.
package gatewayicmp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var (
	ErrUnsupported   = errors.New("gateway ICMP sender is unsupported")
	ErrUnavailable   = errors.New("gateway ICMP source is unavailable")
	ErrBinding       = errors.New("gateway ICMP route or socket binding changed")
	ErrPermission    = errors.New("gateway ICMP source requires permission")
	ErrReceiveBudget = errors.New("gateway ICMP receive budget exhausted; sample incomplete")
	ErrClock         = errors.New("gateway ICMP sample clock is invalid")
	ErrBusy          = errors.New("gateway ICMP sender is busy")
	errWouldBlock    = errors.New("gateway ICMP receive would block")
)

const (
	maxDatagrams    = 16 // Across the whole run, including malformed/foreign replies.
	maxReceiveCalls = 512
	maxPacketBytes  = 512
	maxControlBytes = 256
	pollInterval    = 10 * time.Millisecond
	packetBytes     = 40 // Eight-byte ICMP header plus 32 random payload bytes.
)

// Request comes from trusted controller code AFTER one-shot audited admission.
// It must never be decoded from the client-visible preview. The caller's context
// must retain the original consumed approval deadline, even with newer preflight.
// The gatewayrun adapter preserves samples; no production call site is installed.
type Request struct {
	Plan   networkquality.GatewayCheckPlan
	Source netip.Addr
}

// Sample is not a network-wide verdict. SendCalls includes an uncertain failed
// send syscall; AcceptedRequests means kernel acceptance, not proof of wire egress.
// Only Complete samples have all three reply/timeout outcomes. An error preserves
// partial counts but NEVER populates MeanRTT or licenses a packet-loss inference.
// No packet, nonce, identifier, credential, or raw OS error leaves the sender.
type Sample struct {
	ScopeID                     string
	InterfaceName               string
	InterfaceIndex              int
	Target, Source              netip.Addr
	StartedAt, CompletedAt      time.Time
	SendCalls, AcceptedRequests int
	Replies, Timeouts           int
	Complete                    bool
	MeanRTT                     *time.Duration
}

type routeEvidence struct {
	name                   string
	index                  int
	source                 netip.Addr
	observedAt, freshUntil time.Time
}

type datagram struct {
	data        []byte
	peer, local netip.Addr
	index       int
	truncated   bool
}

// All seams are private and compiled, not caller-provided transport/configuration.
// A socket owns one source/interface/destination, with no retargeting method.
type socket interface {
	verify(context.Context) error
	send(context.Context, []byte) error
	receive(context.Context) (datagram, error)
	close() error
}

type Sender struct {
	busy   atomic.Bool
	lookup func(context.Context, Request) (routeEvidence, error)
	open   func(context.Context, Request) (socket, error)
	now    func() time.Time
	wait   func(context.Context, time.Duration) error
	random io.Reader
}

// NewCandidate constructs no socket and sends nothing. The Darwin implementation
// remains disconnected until owned-lab runtime validation and consent integration.
// Other platforms fail explicitly; there is no raw-socket or executable fallback.
func NewCandidate() *Sender {
	lookup, open := platform()
	return &Sender{lookup: lookup, open: open, now: time.Now, wait: waitContext, random: rand.Reader}
}

// Context timers may use a monotonic clock that stops during system suspend.
// Check the caller's absolute wall deadline too; newer plan evidence must never
// let a resumed sender outlive the original one-shot approval's context.
func contextError(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !now.Round(0).Before(deadline.Round(0)) {
		return context.DeadlineExceeded
	}
	return nil
}

func waitContext(ctx context.Context, duration time.Duration) error {
	if err := contextError(ctx, time.Now()); err != nil {
		return err
	}
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return contextError(ctx, time.Now())
	case <-timer.C:
		return contextError(ctx, time.Now())
	}
}

func validTime(t time.Time) bool {
	return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t)
}

func normalize(r Request, now time.Time) (Request, error) {
	if !validTime(now) || !validTime(r.Plan.CreatedAt) || r.Plan.CreatedAt.After(now) ||
		!r.Plan.ReviewExpiresAt.After(now) || r.Source == r.Plan.Target {
		return Request{}, ErrBinding
	}
	plan, err := networkquality.PreviewGatewayCheck(r.Plan.Binding, r.Plan.Target.String(), r.Plan.CreatedAt)
	if err != nil || plan.Budget != r.Plan.Budget || !plan.ReviewExpiresAt.Equal(r.Plan.ReviewExpiresAt) {
		return Request{}, ErrBinding
	}
	if _, err := networkquality.PreviewGatewayCheck(plan.Binding, r.Source.String(), now); err != nil {
		return Request{}, ErrBinding
	}
	r.Plan = plan // The builder copied and canonicalized the prefix slice.
	return r, nil
}

func copyRequest(r Request) Request {
	r.Plan.Binding.Prefixes = append([]string(nil), r.Plan.Binding.Prefixes...)
	return r
}

func safeError(err error) error {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrUnsupported, ErrPermission,
		ErrBinding, ErrReceiveBudget, ErrClock, ErrBusy} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}

// Measure performs at most three sends. It does not grant consent, audit, retry a
// run, or implement the controller-wide cooldown; those belong to gatewayrun.
// gatewayrun adapts this method and validates/audits its returned measurement.
// The sender itself does not implement gatewayrun.Executor or grant admission.
func (s *Sender) Measure(ctx context.Context, request Request) (sample Sample, err error) {
	if err := contextError(ctx, time.Now()); err != nil {
		return Sample{}, err
	}
	if s == nil || s.lookup == nil || s.open == nil || s.now == nil || s.wait == nil || s.random == nil {
		return Sample{}, ErrUnavailable
	}
	if !s.busy.CompareAndSwap(false, true) {
		return Sample{}, ErrBusy
	}
	defer s.busy.Store(false)
	start := s.now()
	r, err := normalize(request, start.Round(0).UTC())
	if err != nil {
		return Sample{}, err
	}
	budget := r.Plan.Budget
	ctx, cancel := context.WithTimeout(ctx, min(budget.TotalTimeout, r.Plan.ReviewExpiresAt.Sub(start.Round(0).UTC())))
	defer cancel()
	last := start
	clock := func() (time.Time, error) {
		now := s.now()
		if !validTime(now) || now.Round(0).Before(last.Round(0)) || now.Before(last) {
			return time.Time{}, ErrClock
		}
		last = now
		if err := contextError(ctx, time.Now()); err != nil {
			return time.Time{}, err
		}
		if now.Sub(start) >= budget.TotalTimeout || now.Round(0).Sub(start.Round(0)) >= budget.TotalTimeout ||
			!now.Round(0).Before(r.Plan.ReviewExpiresAt) {
			return time.Time{}, context.DeadlineExceeded
		}
		return now, nil
	}
	checkRoute := func() error {
		before, err := clock()
		if err != nil {
			return err
		}
		e, err := s.lookup(ctx, copyRequest(r))
		if err != nil {
			return safeError(err)
		}
		after, err := clock()
		if err != nil {
			return err
		}
		if e.name != r.Plan.Binding.InterfaceName || e.index != r.Plan.Binding.InterfaceIndex || e.source != r.Source ||
			!validTime(e.observedAt) || e.observedAt.Before(before.Round(0)) || e.observedAt.After(after.Round(0)) ||
			!e.freshUntil.Equal(e.observedAt.Add(networkquality.GatewayReviewLifetime)) || !e.freshUntil.After(after.Round(0)) {
			return ErrBinding
		}
		return nil
	}
	// Refuse mismatched/unavailable routes before creating even a data socket.
	if err := checkRoute(); err != nil {
		return Sample{}, err
	}
	var entropy [34]byte
	if _, err := io.ReadFull(s.random, entropy[:]); err != nil {
		return Sample{}, ErrUnavailable
	}
	id := binary.BigEndian.Uint16(entropy[:2])
	var nonce [32]byte
	copy(nonce[:], entropy[2:])
	if _, err := clock(); err != nil {
		return Sample{}, err
	}
	conn, err := s.open(ctx, copyRequest(r))
	if err != nil {
		return Sample{}, safeError(err)
	}
	if conn == nil {
		return Sample{}, ErrUnavailable
	}
	defer func() {
		if closeErr := conn.close(); closeErr != nil {
			sample.Complete, sample.MeanRTT = false, nil
			if err == nil {
				err = ErrUnavailable
			}
		}
	}()
	sample = Sample{ScopeID: r.Plan.Binding.ScopeID, InterfaceName: r.Plan.Binding.InterfaceName,
		InterfaceIndex: r.Plan.Binding.InterfaceIndex, Target: r.Plan.Target, Source: r.Source, StartedAt: start.Round(0).UTC()}
	var lastSend time.Time
	var totalRTT time.Duration
	reads, received := 0, 0
	for seq := 1; seq <= budget.MaxAttempts; seq++ {
		if received >= maxDatagrams || reads >= maxReceiveCalls {
			return sample, ErrReceiveBudget
		}
		now, err := clock()
		if err != nil {
			return sample, err
		}
		if !lastSend.IsZero() {
			if err := s.wait(ctx, max(0, budget.MinInterval-now.Sub(lastSend))); err != nil {
				return sample, safeError(err)
			}
		}
		// Revalidate after pacing and after socket setup, not just during preview.
		if err := checkRoute(); err != nil {
			return sample, err
		}
		if err := conn.verify(ctx); err != nil {
			return sample, safeError(err)
		}
		sentAt, err := clock()
		if err != nil {
			return sample, err
		}
		if !lastSend.IsZero() && sentAt.Sub(lastSend) < budget.MinInterval {
			return sample, ErrClock // A broken wait/clock must not produce a burst.
		}
		packet := echoRequest(id, uint16(seq), nonce)
		sample.SendCalls++ // Errors may be uncertain; never silently resend.
		if err := conn.send(ctx, packet[:]); err != nil {
			return sample, safeError(err)
		}
		sample.AcceptedRequests++
		lastSend, err = clock() // Conservative pacing from syscall completion.
		if err != nil {
			return sample, err
		}
		until := sentAt.Add(budget.AttemptTimeout)
		for {
			now, err := clock()
			if err != nil {
				return sample, err
			}
			if !now.Before(until) {
				sample.Timeouts++
				break
			}
			if received >= maxDatagrams || reads >= maxReceiveCalls {
				return sample, ErrReceiveBudget
			}
			reads++
			d, readErr := conn.receive(ctx)
			now, err = clock()
			if err != nil {
				return sample, err
			}
			if errors.Is(readErr, errWouldBlock) {
				if err := s.wait(ctx, min(pollInterval, max(0, until.Sub(now)))); err != nil {
					return sample, safeError(err)
				}
				continue
			}
			if readErr != nil {
				return sample, safeError(readErr)
			}
			received++
			if !now.Before(until) { // Arrival at the exact deadline is not timely.
				sample.Timeouts++
				break
			}
			if matchesReply(d, r, id, uint16(seq), nonce) {
				sample.Replies++
				totalRTT += now.Sub(sentAt)
				break
			}
		}
	}
	end, err := clock()
	if err != nil {
		return sample, err
	}
	sample.Complete, sample.CompletedAt = true, end.Round(0).UTC()
	if sample.Replies > 0 {
		mean := totalRTT / time.Duration(sample.Replies)
		sample.MeanRTT = &mean
	}
	return sample, nil
}
