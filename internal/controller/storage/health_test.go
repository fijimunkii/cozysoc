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

func TestClassifyStorageHealthUsesKnownQuota(t *testing.T) {
	tests := []struct {
		name string
		used int64
		max  int64
		want HealthState
	}{
		{name: "current", used: 80, max: 100, want: HealthCurrent},
		{name: "pressure threshold", used: 90, max: 100, want: HealthPressure},
		{name: "pressure below quota", used: 99, max: 100, want: HealthPressure},
		{name: "at quota", used: 100, max: 100, want: HealthAtQuota},
		{name: "over quota", used: 101, max: 100, want: HealthAtQuota},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyStorageHealth(test.used, test.max); got != test.want {
				t.Fatalf("storage health used=%d max=%d = %s, want %s", test.used, test.max, got, test.want)
			}
		})
	}
}

func TestStorageHealthReportsEffectiveQuotaAndReusablePages(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	health, err := store.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.State != HealthCurrent || health.MaxBytes <= 0 || health.DatabaseBytes <= 0 || health.UsedBytes <= 0 {
		t.Fatalf("unexpected storage health: %+v", health)
	}
	if health.ReusableBytes < 0 || health.DatabaseBytes != health.UsedBytes+health.ReusableBytes {
		t.Fatalf("invalid allocated/used/reusable accounting: %+v", health)
	}
	if health.UsedBytes > health.MaxBytes {
		t.Fatalf("fresh database already exceeds configured quota: %+v", health)
	}
}
