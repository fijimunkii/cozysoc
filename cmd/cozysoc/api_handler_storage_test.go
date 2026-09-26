package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type storageOverviewFixture struct {
	health storage.Health
	err    error
	policy map[domain.RetentionClass]time.Duration
}

func (f storageOverviewFixture) Health(context.Context) (storage.Health, error) {
	return f.health, f.err
}
func (f storageOverviewFixture) RetentionDurations() map[domain.RetentionClass]time.Duration {
	return f.policy
}

func TestControllerStorageOverviewUsesActivePolicyAndIndependentCapacities(t *testing.T) {
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), &fakeDeviceStore{}, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	readAt := time.Unix(1_800_000_000, 0).UTC()
	handler.now = func() time.Time { return readAt }
	handler.storageOverview = storageOverviewFixture{
		health: storage.Health{QuotaState: storage.HealthPressure, DatabaseBytes: 80, UsedBytes: 70, ReusableBytes: 10, MaxBytes: 100, FilesystemState: storage.FilesystemCapacityFull, FilesystemSupported: true, FilesystemTotalBytes: 1000, FilesystemAvailableBytes: 0},
		policy: map[domain.RetentionClass]time.Duration{
			domain.RetentionEphemeral: time.Hour, domain.RetentionShort: 48 * time.Hour,
			domain.RetentionStandard: 14 * 24 * time.Hour, domain.RetentionAudit: 90 * 24 * time.Hour,
		},
	}
	got, err := handler.StorageOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.AsOf.Equal(readAt) || got.QuotaState != "pressure" || got.FilesystemState != "full" || got.UsedBytes != 70 || got.ReusableBytes != 10 || len(got.Retention) != 4 || got.Retention[2].DurationSeconds != 14*86400 {
		t.Fatalf("storage overview = %+v", got)
	}
	handler.storageOverview = storageOverviewFixture{err: errors.New("private database path")}
	if _, err := handler.StorageOverview(context.Background()); err == nil {
		t.Fatal("expected failed storage read")
	}
}
