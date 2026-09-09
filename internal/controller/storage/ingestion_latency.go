package storage

import (
	"sync"
	"time"
)

const (
	ingestionLatencyThreshold       = 5 * time.Second
	ingestionLatencyFreshnessWindow = time.Minute
	ingestionSlowStreakThreshold    = 3
)

type IngestionLatencyState string

const (
	IngestionLatencyIdle    IngestionLatencyState = "idle"
	IngestionLatencyCurrent IngestionLatencyState = "current"
	IngestionLatencyLagging IngestionLatencyState = "lagging"
)

type ingestionLatencySnapshot struct {
	State                  IngestionLatencyState
	Threshold              time.Duration
	Pending                int
	OldestPendingAge       time.Duration
	LastDurableLatency     time.Duration
	LastQueueWait          time.Duration
	LastProcessingDuration time.Duration
	LastCompletedAt        time.Time
	SlowStreak             int
}

type ingestionLatencyTracker struct {
	mu sync.Mutex

	pending                map[*receiptState]time.Time
	lastDurableLatency     time.Duration
	lastQueueWait          time.Duration
	lastProcessingDuration time.Duration
	lastCompletedAt        time.Time
	slowStreak             int
}

func newIngestionLatencyTracker() *ingestionLatencyTracker {
	return &ingestionLatencyTracker{pending: make(map[*receiptState]time.Time)}
}

func (t *ingestionLatencyTracker) accept(receipt *receiptState, at time.Time) {
	if t == nil || receipt == nil {
		return
	}
	t.mu.Lock()
	t.pending[receipt] = at
	t.mu.Unlock()
}

func (t *ingestionLatencyTracker) complete(receipt *receiptState, acceptedAt, startedAt, completedAt time.Time, durable bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pending, receipt)
	if !durable {
		return
	}

	queueWait := nonNegativeDuration(startedAt.Sub(acceptedAt))
	processing := nonNegativeDuration(completedAt.Sub(startedAt))
	latency := nonNegativeDuration(completedAt.Sub(acceptedAt))

	if !t.lastCompletedAt.IsZero() {
		gap := completedAt.Sub(t.lastCompletedAt)
		if gap < 0 || gap > ingestionLatencyFreshnessWindow {
			t.slowStreak = 0
		}
	}
	if latency >= ingestionLatencyThreshold {
		t.slowStreak++
	} else {
		t.slowStreak = 0
	}
	t.lastDurableLatency = latency
	t.lastQueueWait = queueWait
	t.lastProcessingDuration = processing
	t.lastCompletedAt = completedAt
}

func (t *ingestionLatencyTracker) snapshot(now time.Time) ingestionLatencySnapshot {
	snapshot := ingestionLatencySnapshot{
		State:     IngestionLatencyIdle,
		Threshold: ingestionLatencyThreshold,
	}
	if t == nil {
		return snapshot
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	snapshot.Pending = len(t.pending)
	snapshot.LastDurableLatency = t.lastDurableLatency
	snapshot.LastQueueWait = t.lastQueueWait
	snapshot.LastProcessingDuration = t.lastProcessingDuration
	snapshot.LastCompletedAt = t.lastCompletedAt
	snapshot.SlowStreak = t.slowStreak

	for _, acceptedAt := range t.pending {
		age := nonNegativeDuration(now.Sub(acceptedAt))
		if age > snapshot.OldestPendingAge {
			snapshot.OldestPendingAge = age
		}
	}
	if snapshot.Pending > 0 {
		if snapshot.OldestPendingAge >= ingestionLatencyThreshold {
			snapshot.State = IngestionLatencyLagging
			return snapshot
		}
		snapshot.State = IngestionLatencyCurrent
	}

	if !t.lastCompletedAt.IsZero() {
		age := now.Sub(t.lastCompletedAt)
		if age >= 0 && age <= ingestionLatencyFreshnessWindow {
			if t.slowStreak >= ingestionSlowStreakThreshold {
				snapshot.State = IngestionLatencyLagging
			} else if snapshot.State == IngestionLatencyIdle {
				snapshot.State = IngestionLatencyCurrent
			}
		}
	}
	return snapshot
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}
