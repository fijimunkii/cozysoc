package storage

import (
	"context"
	"testing"
)

func TestIngestionHealthUsesCurrentEpisodesNotHistoricalTotals(t *testing.T) {
	ingestor, err := newIngestor(&fakeIngestionSink{}, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ingestor.Close(context.Background()) }()

	ingestor.stateMu.Lock()
	ingestor.stats.Dropped = 12
	ingestor.stats.Failed = 7
	ingestor.stateMu.Unlock()
	if got := ingestor.Health(); got.State != IngestionHealthCurrent || got.Dropped != 12 || got.Failed != 7 {
		t.Fatalf("historical totals changed current health: %+v", got)
	}

	ingestor.episodeMu.Lock()
	ingestor.overflowActive = true
	ingestor.episodeMu.Unlock()
	if got := ingestor.Health(); got.State != IngestionHealthBackpressure {
		t.Fatalf("active backpressure health = %+v", got)
	}

	ingestor.episodeMu.Lock()
	ingestor.failureActive = true
	ingestor.episodeMu.Unlock()
	if got := ingestor.Health(); got.State != IngestionHealthWriteFailed {
		t.Fatalf("active write failure health = %+v", got)
	}
}

func TestStorageHealthUsesConfiguredDatabaseQuota(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	bytes, err := store.DatabaseBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	store.limits.MaxBytes = bytes * 2
	if got, err := store.Health(ctx); err != nil || got.State != HealthCurrent {
		t.Fatalf("current storage health=%+v err=%v", got, err)
	}

	store.limits.MaxBytes = bytes
	if got, err := store.Health(ctx); err != nil || got.State != HealthAtQuota {
		t.Fatalf("quota storage health=%+v err=%v", got, err)
	}

	store.limits.MaxBytes = bytes * 10 / 9
	if got, err := store.Health(ctx); err != nil || got.State != HealthPressure {
		t.Fatalf("pressure storage health=%+v err=%v", got, err)
	}
}
