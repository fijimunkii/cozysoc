package storage

import (
	"testing"
	"time"
)

func TestIngestionHealthKeepsQueueUtilizationSeparateFromLag(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	ingestor := &Ingestor{
		queue:   make(chan ingestionItem, 4),
		now:     func() time.Time { return now },
		latency: newIngestionLatencyTracker(),
		stats:   IngestionStats{Capacity: 4},
	}
	for index := 0; index < 3; index++ {
		ingestor.queue <- ingestionItem{}
	}

	got := ingestor.Health()
	if got.State != IngestionHealthCurrent || !got.QueuePressure || got.Depth != 3 || got.LatencyState != IngestionLatencyIdle {
		t.Fatalf("queue utilization became health degradation: %+v", got)
	}
}

func TestIngestionHealthPromotesMeasuredPendingLag(t *testing.T) {
	base := time.Unix(1_800_000_000, 0).UTC()
	tracker := newIngestionLatencyTracker()
	tracker.accept(base)
	ingestor := &Ingestor{
		queue:   make(chan ingestionItem, 4),
		now:     func() time.Time { return base.Add(ingestionLatencyThreshold + time.Second) },
		latency: tracker,
		stats:   IngestionStats{Capacity: 4},
	}

	got := ingestor.Health()
	if got.State != IngestionHealthLagging || got.LatencyState != IngestionLatencyLagging || got.OldestPendingAge != ingestionLatencyThreshold+time.Second {
		t.Fatalf("measured pending lag = %+v", got)
	}
}
