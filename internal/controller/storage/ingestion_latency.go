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

	pending                []time.Time
	lastDurableLatency     time.Duration
	lastQueueWait          time.Duration
	lastProcessingDuration time.Duration
	lastCompletedAt        time.Time
	slowStreak             int
}

func newIngestionLatencyTracker() *ingestionLatencyTracker {
	return &ingestionLatencyTracker{}
}

func (t *ingestionLatencyTracker) accept(at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.pending = append(t.pending, at)
	t.mu.Unlock()
}

func (t *ingestionLatencyTracker) complete(acceptedAt, startedAt, completedAt time.Time, durable bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.pending) > 0 {
		t.pending = t.pending[1:]
	}
	if !durable {
		return
	}

	queueWait := nonNegativeDuration(startedAt.Sub(acceptedAt))
	processing := nonNegativeDuration(completedAt.Sub(startedAt))
	latency := nonNegativeDuration(completedAt.Sub(acceptedAt))

	if !t.lastCompletedAt.IsZero() && completedAt.Sub(t.lastCompletedAt) > ingestionLatencyFreshnessWindow {
		t.slowStreak = 0
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

	if len(t.pending) > 0 {
		snapshot.OldestPendingAge = nonNegativeDuration(now.Sub(t.pending[0]))
		if snapshot.OldestPendingAge >= ingestionLatencyThreshold {
			snapshot.State = IngestionLatencyLagging
			return snapshot
		}
		snapshot.State = IngestionLatencyCurrent
	}

	if !t.lastCompletedAt.IsZero() && nonNegativeDuration(now.Sub(t.lastCompletedAt)) <= ingestionLatencyFreshnessWindow {
		if t.slowStreak >= ingestionSlowStreakThreshold {
			snapshot.State = IngestionLatencyLagging
		} else if snapshot.State == IngestionLatencyIdle {
			snapshot.State = IngestionLatencyCurrent
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
