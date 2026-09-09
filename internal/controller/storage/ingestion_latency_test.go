package storage

import (
	"testing"
	"time"
)

func TestIngestionLatencyTrackerMeasuresAcceptedToDurableTiming(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	accepted := time.Unix(1_800_000_000, 0).UTC()
	started := accepted.Add(200 * time.Millisecond)
	completed := started.Add(300 * time.Millisecond)

	tracker.accept(accepted)
	tracker.complete(accepted, started, completed, true)

	got := tracker.snapshot(completed)
	if got.State != IngestionLatencyCurrent || got.Pending != 0 {
		t.Fatalf("latency state = %+v", got)
	}
	if got.LastQueueWait != 200*time.Millisecond || got.LastProcessingDuration != 300*time.Millisecond || got.LastDurableLatency != 500*time.Millisecond {
		t.Fatalf("latency measurements = %+v", got)
	}
}

func TestIngestionLatencyTrackerRequiresSustainedCompletedLag(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	base := time.Unix(1_800_000_000, 0).UTC()

	for index := 0; index < ingestionSlowStreakThreshold-1; index++ {
		accepted := base.Add(time.Duration(index) * 10 * time.Second)
		completed := accepted.Add(ingestionLatencyThreshold + time.Second)
		tracker.accept(accepted)
		tracker.complete(accepted, accepted, completed, true)
		if got := tracker.snapshot(completed); got.State == IngestionLatencyLagging {
			t.Fatalf("latency degraded before sustained streak: %+v", got)
		}
	}

	accepted := base.Add(20 * time.Second)
	completed := accepted.Add(ingestionLatencyThreshold + time.Second)
	tracker.accept(accepted)
	tracker.complete(accepted, accepted, completed, true)
	if got := tracker.snapshot(completed); got.State != IngestionLatencyLagging || got.SlowStreak != ingestionSlowStreakThreshold {
		t.Fatalf("sustained latency did not degrade: %+v", got)
	}

	fastAccepted := completed.Add(time.Second)
	fastCompleted := fastAccepted.Add(100 * time.Millisecond)
	tracker.accept(fastAccepted)
	tracker.complete(fastAccepted, fastAccepted, fastCompleted, true)
	if got := tracker.snapshot(fastCompleted); got.State != IngestionLatencyCurrent || got.SlowStreak != 0 {
		t.Fatalf("successful fast write did not recover latency health: %+v", got)
	}
}

func TestIngestionLatencyTrackerDetectsOldPendingRecord(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	accepted := time.Unix(1_800_000_000, 0).UTC()
	tracker.accept(accepted)

	beforeThreshold := tracker.snapshot(accepted.Add(ingestionLatencyThreshold - time.Millisecond))
	if beforeThreshold.State != IngestionLatencyCurrent {
		t.Fatalf("pending record degraded early: %+v", beforeThreshold)
	}

	atThreshold := tracker.snapshot(accepted.Add(ingestionLatencyThreshold))
	if atThreshold.State != IngestionLatencyLagging || atThreshold.OldestPendingAge != ingestionLatencyThreshold {
		t.Fatalf("oldest pending latency = %+v", atThreshold)
	}
}

func TestIngestionLatencyTrackerTreatsQuietOldMeasurementsAsIdle(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	accepted := time.Unix(1_800_000_000, 0).UTC()
	completed := accepted.Add(ingestionLatencyThreshold + time.Second)

	for index := 0; index < ingestionSlowStreakThreshold; index++ {
		at := accepted.Add(time.Duration(index) * time.Second)
		done := at.Add(ingestionLatencyThreshold + time.Second)
		tracker.accept(at)
		tracker.complete(at, at, done, true)
		completed = done
	}
	if got := tracker.snapshot(completed.Add(ingestionLatencyFreshnessWindow + time.Second)); got.State != IngestionLatencyIdle {
		t.Fatalf("quiet pipeline stayed degraded from old latency: %+v", got)
	}
}

func TestFailedCompletionDoesNotBecomeDurableLatencyEvidence(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	accepted := time.Unix(1_800_000_000, 0).UTC()
	tracker.accept(accepted)
	tracker.complete(accepted, accepted, accepted.Add(ingestionLatencyThreshold+time.Second), false)

	got := tracker.snapshot(accepted.Add(ingestionLatencyThreshold + time.Second))
	if got.Pending != 0 || got.LastDurableLatency != 0 || got.SlowStreak != 0 || got.State != IngestionLatencyIdle {
		t.Fatalf("failed write became durable latency evidence: %+v", got)
	}
}
