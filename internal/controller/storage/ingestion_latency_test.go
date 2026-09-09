package storage

import (
	"testing"
	"time"
)

func TestIngestionLatencyTrackerMeasuresAcceptedToDurableTiming(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	receipt := &receiptState{}
	accepted := time.Unix(1_800_000_000, 0).UTC()
	started := accepted.Add(200 * time.Millisecond)
	completed := started.Add(300 * time.Millisecond)

	tracker.accept(receipt, accepted)
	tracker.complete(receipt, accepted, started, completed, true)

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
		receipt := &receiptState{}
		accepted := base.Add(time.Duration(index) * 10 * time.Second)
		completed := accepted.Add(ingestionLatencyThreshold + time.Second)
		tracker.accept(receipt, accepted)
		tracker.complete(receipt, accepted, accepted, completed, true)
		if got := tracker.snapshot(completed); got.State == IngestionLatencyLagging {
			t.Fatalf("latency degraded before sustained streak: %+v", got)
		}
	}

	receipt := &receiptState{}
	accepted := base.Add(20 * time.Second)
	completed := accepted.Add(ingestionLatencyThreshold + time.Second)
	tracker.accept(receipt, accepted)
	tracker.complete(receipt, accepted, accepted, completed, true)
	if got := tracker.snapshot(completed); got.State != IngestionLatencyLagging || got.SlowStreak != ingestionSlowStreakThreshold {
		t.Fatalf("sustained latency did not degrade: %+v", got)
	}

	fastReceipt := &receiptState{}
	fastAccepted := completed.Add(time.Second)
	fastCompleted := fastAccepted.Add(100 * time.Millisecond)
	tracker.accept(fastReceipt, fastAccepted)
	tracker.complete(fastReceipt, fastAccepted, fastAccepted, fastCompleted, true)
	if got := tracker.snapshot(fastCompleted); got.State != IngestionLatencyCurrent || got.SlowStreak != 0 {
		t.Fatalf("successful fast write did not recover latency health: %+v", got)
	}
}

func TestIngestionLatencyTrackerDetectsOldPendingRecord(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	receipt := &receiptState{}
	accepted := time.Unix(1_800_000_000, 0).UTC()
	tracker.accept(receipt, accepted)

	beforeThreshold := tracker.snapshot(accepted.Add(ingestionLatencyThreshold - time.Millisecond))
	if beforeThreshold.State != IngestionLatencyCurrent {
		t.Fatalf("pending record degraded early: %+v", beforeThreshold)
	}

	atThreshold := tracker.snapshot(accepted.Add(ingestionLatencyThreshold))
	if atThreshold.State != IngestionLatencyLagging || atThreshold.OldestPendingAge != ingestionLatencyThreshold {
		t.Fatalf("oldest pending latency = %+v", atThreshold)
	}
}

func TestIngestionLatencyTrackerRemovesTheCompletedReceipt(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	base := time.Unix(1_800_000_000, 0).UTC()
	older := &receiptState{}
	newer := &receiptState{}
	tracker.accept(older, base)
	tracker.accept(newer, base.Add(time.Second))

	tracker.complete(newer, base.Add(time.Second), base.Add(2*time.Second), base.Add(3*time.Second), true)
	got := tracker.snapshot(base.Add(ingestionLatencyThreshold + time.Second))
	if got.Pending != 1 || got.OldestPendingAge != ingestionLatencyThreshold+time.Second || got.State != IngestionLatencyLagging {
		t.Fatalf("completion removed the wrong pending receipt: %+v", got)
	}
}

func TestIngestionLatencyTrackerTreatsQuietOldMeasurementsAsIdle(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	accepted := time.Unix(1_800_000_000, 0).UTC()
	completed := accepted.Add(ingestionLatencyThreshold + time.Second)

	for index := 0; index < ingestionSlowStreakThreshold; index++ {
		receipt := &receiptState{}
		at := accepted.Add(time.Duration(index) * time.Second)
		done := at.Add(ingestionLatencyThreshold + time.Second)
		tracker.accept(receipt, at)
		tracker.complete(receipt, at, at, done, true)
		completed = done
	}
	if got := tracker.snapshot(completed.Add(ingestionLatencyFreshnessWindow + time.Second)); got.State != IngestionLatencyIdle {
		t.Fatalf("quiet pipeline stayed degraded from old latency: %+v", got)
	}
}

func TestFailedCompletionDoesNotBecomeDurableLatencyEvidence(t *testing.T) {
	tracker := newIngestionLatencyTracker()
	receipt := &receiptState{}
	accepted := time.Unix(1_800_000_000, 0).UTC()
	tracker.accept(receipt, accepted)
	tracker.complete(receipt, accepted, accepted, accepted.Add(ingestionLatencyThreshold+time.Second), false)

	got := tracker.snapshot(accepted.Add(ingestionLatencyThreshold + time.Second))
	if got.Pending != 0 || got.LastDurableLatency != 0 || got.SlowStreak != 0 || got.State != IngestionLatencyIdle {
		t.Fatalf("failed write became durable latency evidence: %+v", got)
	}
}
