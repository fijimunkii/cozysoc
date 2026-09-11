package gatewayicmp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestContextWallDeadlineExpiresWithoutTimerCancellation(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	// Simulate wall time advancing during suspend while the real context timer
	// has not fired. Exact expiry must stop the sender independently of ctx.Err.
	if ctx.Err() != nil {
		t.Fatal("fixture timer already fired")
	}
	if err := contextError(ctx, deadline.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{deadline, deadline.Add(time.Second)} {
		if !errors.Is(contextError(ctx, now), context.DeadlineExceeded) {
			t.Fatal("absolute caller deadline was extended after suspend")
		}
	}
	cancel()
	if !errors.Is(contextError(ctx, deadline.Add(-time.Second)), context.Canceled) {
		t.Fatal("lost explicit cancellation")
	}
	if err := contextError(context.Background(), deadline); err != nil {
		t.Fatal(err)
	}
}

func TestWaitHonorsCancellationAndZeroDelay(t *testing.T) {
	if err := waitContext(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := waitContext(context.Background(), time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(waitContext(ctx, time.Hour), context.Canceled) {
		t.Fatal("wait ignored cancellation")
	}
}
