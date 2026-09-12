package resolverrun

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// Dependencies are compiled controller collaborators, never caller-supplied
// configuration. Construct exactly one Control per controller lifetime. Now is a
// test seam; production defaults to time.Now, retaining its monotonic component.
// Missing collaborators deliberately leave the control unavailable.
type Dependencies struct {
	Preflight Preflight
	Executor  Executor
	Auditor   Auditor
	Now       func() time.Time
}
type pendingReview struct {
	ticket    Ticket
	selection Selection
	expiresAt time.Time
	deadline  time.Time // process-local monotonic deadline when available
	runID     string
}
type Control struct {
	mu           sync.Mutex
	deps         Dependencies
	random       io.Reader
	pending      *pendingReview
	busy, closed bool
	fault        error
	lastWall     time.Time
	notBefore    time.Time
	activeCancel context.CancelFunc
	drained      chan struct{} // Closed once shutdown has no remaining collaborators.
}

func New(deps Dependencies) (*Control, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	now := deps.Now()
	if !validTime(now) || !validTime(now.Add(RunInterval)) {
		return nil, ErrClock
	}
	return &Control{deps: deps, random: rand.Reader, lastWall: now.Round(0).UTC(), notBefore: now.Add(RunInterval), drained: make(chan struct{})}, nil
}

// clockLocked preserves a wall-clock high-water mark as well as Go's in-process
// monotonic times. Rollback permanently locks this instance; never revive an old
// review when a wall clock later catches up. Restart has a new full quiet minute.
func (c *Control) clockLocked() (time.Time, error) {
	if c.fault != nil {
		return time.Time{}, c.fault
	}
	now := c.deps.Now()
	wall := now.Round(0).UTC()
	if !validTime(now) || !validTime(now.Add(RunInterval)) || wall.Before(c.lastWall) {
		c.fault = ErrClock
		c.pending = nil
		if c.activeCancel != nil {
			c.activeCancel()
		}
		return time.Time{}, ErrClock
	}
	c.lastWall = wall
	return now, nil
}
func (c *Control) availableLocked() error {
	if c.fault != nil {
		return c.fault
	}
	if c.closed || c.deps.Preflight == nil || c.deps.Executor == nil || c.deps.Auditor == nil {
		return ErrUnavailable
	}
	return nil
}
func (c *Control) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy = false
	c.activeCancel = nil
	c.signalDrainedLocked()
}

func (c *Control) signalDrainedLocked() {
	if c.closed && !c.busy {
		select {
		case <-c.drained:
		default:
			close(c.drained)
		}
	}
}
func (c *Control) clock() (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clockLocked()
}
func (p pendingReview) live(now time.Time) bool {
	return now.Round(0).UTC().Before(p.expiresAt) && now.Before(p.deadline)
}
func overlong(start, now time.Time) bool {
	return now.Sub(start) >= OperationTimeout || now.Round(0).Sub(start.Round(0)) >= OperationTimeout
}

// Prepare performs bounded metadata preflight, with at most one operation and
// one outstanding review. It records NO consent, audits, monitoring or packets.
// The thirty-second ceiling starts at evidence time, not response arrival.
func (c *Control) Prepare(ctx context.Context, selectionID string) (Review, error) {
	if err := ctx.Err(); err != nil {
		return Review{}, err
	}
	if !validID(selectionID) {
		return Review{}, ErrPreflight
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	c.mu.Lock()
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return Review{}, err
	}
	start, err := c.clockLocked()
	if err != nil {
		c.mu.Unlock()
		return Review{}, err
	}
	if c.busy {
		c.mu.Unlock()
		return Review{}, ErrBusy
	}
	if start.Before(c.notBefore) {
		c.mu.Unlock()
		return Review{}, ErrCooldown
	}
	if c.pending != nil && c.pending.live(start) {
		c.mu.Unlock()
		return Review{}, ErrBusy
	}
	c.pending = nil
	c.busy = true
	c.activeCancel = cancel
	c.mu.Unlock()
	defer c.release()
	selected, err := safePreflight(c.deps.Preflight, ctx, selectionID)
	if err != nil {
		return Review{}, ErrPreflight
	}
	// Generate separate random identifiers for the redacted process-local ticket
	// and persistent non-authorizing run correlation. No ticket enters the audit.
	var ticket Ticket
	var run [16]byte
	if _, err := io.ReadFull(c.random, ticket.key[:]); err != nil {
		return Review{}, ErrUnavailable
	}
	if _, err := io.ReadFull(c.random, run[:]); err != nil || ticket == (Ticket{}) {
		return Review{}, ErrUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.clockLocked()
	if err != nil {
		return Review{}, err
	}
	if err := c.availableLocked(); err != nil {
		return Review{}, err
	}
	if err := ctx.Err(); err != nil {
		return Review{}, err
	}
	if overlong(start, now) {
		return Review{}, ErrPreflight
	}
	selected, err = normalize(selected, selectionID, now.Round(0).UTC())
	if err != nil || selected.RouteObservedAt.Before(start.Round(0).UTC()) {
		return Review{}, ErrPreflight
	}
	expires := selected.Plan.Disclosure().ExpiresAt
	if selected.RouteFreshUntil.Before(expires) {
		expires = selected.RouteFreshUntil
	}
	c.pending = &pendingReview{ticket: ticket, selection: copySelection(selected), expiresAt: expires,
		deadline: now.Add(expires.Sub(now.Round(0).UTC())), runID: hex.EncodeToString(run[:])}
	return Review{Ticket: ticket, Selection: copySelection(selected), ExpiresAt: expires}, nil
}

// Run consumes one live ticket exactly once after explicit consent. Reservation
// and the controller-wide cooldown happen under one mutex BEFORE audit/preflight.
// Revalidation failure, cancellation, panic or uncertain audit never restores it.
// A durable authorized event precedes revalidation; a durable admitted event must
// precede the executor. Neither event asserts that any packet was actually sent.
func (c *Control) Run(ctx context.Context, ticket Ticket, consent bool) (Result, error) {
	if !consent {
		return Result{}, ErrConsent
	}
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	c.mu.Lock()
	if err := c.availableLocked(); err != nil {
		c.mu.Unlock()
		return Result{}, err
	}
	start, err := c.clockLocked()
	if err != nil {
		c.mu.Unlock()
		return Result{}, err
	}
	if c.pending == nil || subtle.ConstantTimeCompare(ticket.key[:], c.pending.ticket.key[:]) != 1 {
		c.mu.Unlock()
		return Result{}, ErrReview
	}
	approved := *c.pending
	c.pending = nil // Consume even if now expired or canceled.
	if !approved.live(start) {
		c.mu.Unlock()
		return Result{}, ErrReview
	}
	if c.busy || start.Before(c.notBefore) {
		c.mu.Unlock()
		return Result{}, ErrBusy
	}
	c.busy = true
	c.notBefore = start.Add(RunInterval)
	c.activeCancel = cancel
	// The consumed review bounds the entire operation, not just admission. Fresh
	// preflight must not give a future sender time beyond the original consent.
	remaining := min(approved.expiresAt.Sub(start.Round(0).UTC()), approved.deadline.Sub(start))
	ctx, reviewCancel := context.WithTimeout(ctx, remaining)
	defer reviewCancel()
	c.mu.Unlock()
	defer c.release()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := c.record(ctx, approved, "authorized", "", "", nil); err != nil {
		return Result{}, err
	}
	var measured *nq.ResolverMeasurement
	finish := func(outcome, reason string, cause error) (Result, error) {
		// Finish synchronously within a separate cleanup deadline even on caller
		// cancellation. This does not detach a worker or continue sending after cancel.
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), AuditTimeout)
		defer stop()
		if err := c.record(cleanup, approved, "finished", outcome, reason, measured); err != nil {
			return Result{}, err
		}
		return Result{RunID: approved.runID, Outcome: outcome, Sample: copySample(measured)}, cause
	}
	if ctx.Err() != nil {
		return finish("canceled", "canceled", ctx.Err())
	}
	fresh, err := safePreflight(c.deps.Preflight, ctx, approved.selection.Plan.Disclosure().Configuration.Selection.ID)
	if ctx.Err() != nil {
		return finish("canceled", "canceled", ctx.Err())
	}
	if err != nil {
		return finish("blocked", "preflight-unavailable", ErrPreflight)
	}
	now, err := c.clock()
	if err != nil {
		return Result{}, err
	}
	if !approved.live(now) || overlong(start, now) {
		return finish("blocked", "review-expired", ErrReview)
	}
	fresh, err = normalize(fresh, approved.selection.Plan.Disclosure().Configuration.Selection.ID, now.Round(0).UTC())
	if err != nil || fresh.RouteObservedAt.Before(start.Round(0).UTC()) {
		return finish("blocked", "preflight-unavailable", ErrPreflight)
	}
	if !approved.selection.Plan.SameSelection(fresh.Plan) {
		return finish("blocked", "selection-changed", ErrPreflight)
	}
	if err := c.record(ctx, approved, "admitted", "", "", nil); err != nil {
		return Result{}, err
	}
	// Audit I/O may consume the last instant of freshness. Recheck it and cancellation
	// AFTER durable admission, before handing over to the trusted narrow executor.
	now, err = c.clock()
	if err != nil {
		return Result{}, err
	}
	if ctx.Err() != nil {
		return finish("canceled", "canceled", ctx.Err())
	}
	if !approved.live(now) || overlong(start, now) {
		return finish("blocked", "review-expired", ErrReview)
	}
	executionStarted := now.Round(0).UTC()
	sample, executionErr, panicked := safeExecute(c.deps.Executor, ctx, copySelection(fresh), approved.runID)
	if panicked {
		return finish("indeterminate", "execution-panic", ErrExecution)
	}
	now, err = c.clock()
	if err != nil {
		return Result{}, err
	}
	// Validate before retaining or auditing any sender-controlled measurement.
	// Current cancellation is independent of whether a sample already completed.
	measured, err = validateSample(sample, executionErr, fresh, approved.runID, executionStarted, now.Round(0).UTC(), approved.expiresAt)
	if err != nil {
		return finish("failed", "measurement-invalid", ErrExecution)
	}
	if ctx.Err() != nil {
		return finish("canceled", "canceled", ctx.Err())
	}
	if !approved.live(now) || overlong(start, now) {
		return finish("canceled", "canceled", context.DeadlineExceeded)
	}
	if executionErr == context.Canceled {
		return finish("canceled", "canceled", context.Canceled)
	}
	if executionErr == context.DeadlineExceeded {
		return finish("canceled", "canceled", context.DeadlineExceeded)
	}
	if executionErr != nil {
		return finish("failed", "execution-error", ErrExecution)
	}
	return finish("completed", "", nil)
}

func (c *Control) record(ctx context.Context, p pendingReview, state, outcome, reason string, sample *nq.ResolverMeasurement) error {
	now, err := c.clock()
	if err != nil {
		return err
	} // Do not invent an event time after clock failure.
	s := p.selection
	d := s.Plan.Disclosure()
	event := Event{SchemaVersion: EventSchemaVersion, Measurement: auditMeasurement(sample), RunID: p.runID, State: state, Outcome: outcome, Reason: reason, At: now.Round(0).UTC(), Profile: Profile,
		Selection: d.Configuration.Selection, Observer: d.Binding.Observer}
	if err := safeAudit(c.deps.Auditor, ctx, event); err != nil || ctx.Err() != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.fault = ErrAudit
		c.pending = nil
		if c.activeCancel != nil {
			c.activeCancel()
		}
		return ErrAudit
	}
	return nil
}

// Close invalidates pending consent and requests active cancellation. It does not
// release an active reservation early. Use Shutdown to join active preflight,
// execution and terminal audit work before closing their storage dependencies.
func (c *Control) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.pending = nil
	if c.activeCancel != nil {
		c.activeCancel()
	}
	c.signalDrainedLocked()
}

// Shutdown irreversibly closes admission, cancels active work, then joins it.
// Success means no collaborator can still execute or write an audit. Context
// expiry bounds only this wait: it NEVER releases the reservation, detaches a
// worker, reopens admission, or licenses closing storage under an active run.
// A later call can finish waiting. Concurrent callers share the same drain signal.
// Do not invoke synchronously from a collaborator; it would wait on itself.
func (c *Control) Shutdown(ctx context.Context) error {
	c.Close()
	// Prefer already-complete shutdown over an expired wait context.
	select {
	case <-c.drained:
		return nil
	default:
	}
	select {
	case <-c.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func safePreflight(f Preflight, ctx context.Context, selectionID string) (s Selection, err error) {
	defer func() {
		if recover() != nil {
			s = Selection{}
			err = ErrPreflight
		}
	}()
	return f(ctx, selectionID)
}
func safeExecute(e Executor, ctx context.Context, s Selection, runID string) (sample nq.ResolverMeasurement, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			sample = nq.ResolverMeasurement{}
			err = ErrExecution
			panicked = true
		}
	}()
	sample, err = e.ExecuteResolver(ctx, Request{Selection: s, MeasurementID: runID})
	// Error classification can call collaborator-defined Is/Unwrap methods, so
	// keep it inside the same panic boundary as execution. Never retain raw errors.
	switch {
	case errors.Is(err, context.Canceled):
		err = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		err = context.DeadlineExceeded
	case err != nil:
		err = ErrExecution
	}
	return sample, err, false
}
func safeAudit(a Auditor, ctx context.Context, e Event) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrAudit
		}
	}()
	if err := ValidateEvent(e); err != nil {
		return err
	}
	return a.InsertResolverRunAudit(ctx, e)
}
