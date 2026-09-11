package gatewayrun

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
)

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture synchronization timed out")
	}
}

func TestShutdownIdleAndPendingReviewIsPermanentAndIdempotent(t *testing.T) {
	for _, pending := range []bool{false, true} {
		c, _, audit, calls := fixture(t)
		var review Review
		if pending {
			review = prepare(t, c)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// Already-drained shutdown succeeds even with an expired wait context.
		for i := 0; i < 3; i++ {
			if err := c.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.Prepare(context.Background(), testTarget); err != ErrUnavailable {
			t.Fatal("shutdown reopened prepare", err)
		}
		if _, err := c.Run(context.Background(), review.Ticket, true); err != ErrUnavailable {
			t.Fatal("shutdown retained approval", err)
		}
		if calls.Load() != 0 || len(audit.copy()) != 0 {
			t.Fatal("idle shutdown created audit or execution")
		}
	}
}

func TestShutdownWaitsForPreflightEvenWhenItIgnoresCancellation(t *testing.T) {
	c, clock, audit, calls := fixture(t)
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	c.deps.Preflight = func(ctx context.Context, target netip.Addr) (Selection, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return selectionAt(clock.now(), target), nil
	}
	done := make(chan error, 1)
	go func() { _, err := c.Prepare(context.Background(), testTarget); done <- err }()
	waitSignal(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Shutdown(ctx); err != context.Canceled {
		t.Fatal("unfinished preflight reported drained", err)
	}
	waitSignal(t, canceled)
	select {
	case <-c.drained:
		t.Fatal("shutdown detached preflight")
	default:
	}
	if _, err := c.Prepare(context.Background(), testTarget); err != ErrUnavailable {
		t.Fatal("closed admission accepted concurrent work", err)
	}
	close(release)
	if err := <-done; err != ErrUnavailable {
		t.Fatal("late preflight resurrected review", err)
	}
	if err := c.Shutdown(context.Background()); err != nil || calls.Load() != 0 || len(audit.copy()) != 0 {
		t.Fatal("preflight did not drain without side effects", err)
	}
}

func TestShutdownJoinsTerminalAuditAndAllConcurrentWaiters(t *testing.T) {
	c, _, audit, _ := fixture(t)
	entered := make(chan struct{})
	auditEntered, releaseAudit := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseAudit:
		default:
			close(releaseAudit)
		}
	})
	c.deps.Executor = executorFunc(func(ctx context.Context, _ Selection) (gatewayicmp.Sample, error) {
		close(entered)
		<-ctx.Done()
		return gatewayicmp.Sample{}, ctx.Err()
	})
	c.deps.Auditor = auditorFunc(func(ctx context.Context, e Event) error {
		if e.State == "finished" {
			if ctx.Err() != nil {
				return errors.New("terminal cleanup inherited cancellation")
			}
			close(auditEntered)
			<-releaseAudit
		}
		return audit.InsertGatewayRunAudit(ctx, e)
	})
	r := prepare(t, c)
	type execution struct {
		result Result
		err    error
	}
	done := make(chan execution, 1)
	go func() { result, err := c.Run(context.Background(), r.Ticket, true); done <- execution{result, err} }()
	waitSignal(t, entered)
	c.Close()
	waitSignal(t, auditEntered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Shutdown(ctx); err != context.Canceled {
		t.Fatal("audit still active but shutdown succeeded", err)
	}
	c.mu.Lock()
	busy := c.busy
	c.mu.Unlock()
	if !busy {
		t.Fatal("wait cancellation released the execution reservation")
	}
	const count = 8
	waiters := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() { waiters <- c.Shutdown(context.Background()) }()
	}
	select {
	case <-c.drained:
		t.Fatal("audit storage could be closed before terminal commit")
	default:
	}
	close(releaseAudit)
	for i := 0; i < count; i++ {
		if err := <-waiters; err != nil {
			t.Fatal(err)
		}
	}
	got := <-done
	if got.err != context.Canceled || got.result.Outcome != "canceled" || len(audit.copy()) != 3 {
		t.Fatalf("terminal evidence did not finish before drain: %+v %v", got.result, got.err)
	}
	if _, err := c.Run(context.Background(), r.Ticket, true); err != ErrUnavailable {
		t.Fatal("drained controller accepted replay", err)
	}
}

func TestShutdownCanDrainFailedTerminalAuditWithoutClaimingSuccess(t *testing.T) {
	c, _, audit, _ := fixture(t)
	entered := make(chan struct{})
	c.deps.Executor = executorFunc(func(ctx context.Context, _ Selection) (gatewayicmp.Sample, error) {
		close(entered)
		<-ctx.Done()
		return gatewayicmp.Sample{}, ctx.Err()
	})
	audit.failAt = 3
	r := prepare(t, c)
	done := make(chan error, 1)
	go func() {
		result, err := c.Run(context.Background(), r.Ticket, true)
		if result != (Result{}) {
			done <- errors.New("published unconfirmed terminal evidence")
			return
		}
		done <- err
	}()
	waitSignal(t, entered)
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != ErrAudit || len(audit.copy()) != 2 {
		t.Fatal("drained status hid audit failure", err)
	}
}

func TestPrepareShutdownRaceNeverReopensDrainedControl(t *testing.T) {
	for i := 0; i < 100; i++ {
		c, _, _, calls := fixture(t)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = c.Prepare(context.Background(), testTarget) }()
		go func() { defer wg.Done(); _ = c.Shutdown(context.Background()) }()
		wg.Wait()
		if err := c.Shutdown(context.Background()); err != nil || calls.Load() != 0 {
			t.Fatal("shutdown race executed packets", err)
		}
		if _, err := c.Prepare(context.Background(), testTarget); err != ErrUnavailable {
			t.Fatal("race resurrected admission", err)
		}
	}
}
