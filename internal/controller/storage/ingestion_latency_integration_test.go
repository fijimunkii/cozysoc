package storage

import (
	"context"
	"sync"
	"testing"
	"time"
)

type ingestionTestClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *ingestionTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *ingestionTestClock) Set(at time.Time) {
	c.mu.Lock()
	c.at = at
	c.mu.Unlock()
}

func TestIngestorHealthDetectsAcceptedPendingLag(t *testing.T) {
	sink := newFakeIngestionSink()
	sink.blockObservation = true
	ingestor, err := newIngestor(sink, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1_800_000_000, 0).UTC()
	clock := &ingestionTestClock{at: base}
	ingestor.now = clock.Now

	receipt, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.latency"), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-sink.started

	clock.Set(base.Add(ingestionLatencyThreshold + time.Second))
	lagging := ingestor.Health()
	if lagging.State != IngestionHealthLagging || lagging.LatencyState != IngestionLatencyLagging || lagging.Pending != 1 || lagging.OldestPendingAge != ingestionLatencyThreshold+time.Second {
		t.Fatalf("accepted pending write did not become lagging: %+v", lagging)
	}

	close(sink.release)
	if _, err := receipt.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := ingestor.Health()
	if recovered.State != IngestionHealthCurrent || recovered.Pending != 0 || recovered.LastDurableLatency != ingestionLatencyThreshold+time.Second || recovered.SlowStreak != 1 {
		t.Fatalf("completed write latency/recovery = %+v", recovered)
	}
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
