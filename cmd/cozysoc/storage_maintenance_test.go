package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type fakeStorageMaintenanceStore struct {
	healths []storage.Health
	results []*storage.EvidenceBatchRetentionResult
	err     error
	health  int
	prunes  int
	times   []time.Time
}

type blockingStartupMaintenanceStore struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingStartupMaintenanceStore) Health(context.Context) (storage.Health, error) {
	return storage.Health{QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityCurrent}, nil
}

func (s *blockingStartupMaintenanceStore) PruneEvidenceBatchExpired(context.Context, time.Time, int, int) (*storage.EvidenceBatchRetentionResult, error) {
	close(s.started)
	<-s.release
	return &storage.EvidenceBatchRetentionResult{}, nil
}

func TestStartupMaintenanceFinishesBeforeRequestsCanBeServed(t *testing.T) {
	store := &blockingStartupMaintenanceStore{started: make(chan struct{}), release: make(chan struct{})}
	returned := make(chan struct{})
	go func() {
		stop, err := startStorageMaintenance(context.Background(), store, nil)
		if err == nil {
			stop()
		}
		close(returned)
	}()
	select {
	case <-store.started:
	case <-time.After(2 * time.Second):
		close(store.release)
		t.Fatal("startup retention pass never began")
	}
	select {
	case <-returned:
		close(store.release)
		t.Fatal("maintenance returned before its first pass completed")
	default:
	}
	close(store.release)
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not finish after startup pass")
	}
}

func (f *fakeStorageMaintenanceStore) Health(context.Context) (storage.Health, error) {
	index := f.health
	f.health++
	if index >= len(f.healths) {
		index = len(f.healths) - 1
	}
	return f.healths[index], nil
}

func (f *fakeStorageMaintenanceStore) PruneEvidenceBatchExpired(_ context.Context, now time.Time, rows, batches int) (*storage.EvidenceBatchRetentionResult, error) {
	if rows != storageMaintenanceMaxLegacyRows || batches != storageMaintenanceMaxBatches {
		return nil, errors.New("maintenance limits changed")
	}
	f.times = append(f.times, now)
	index := f.prunes
	f.prunes++
	if f.err != nil {
		return nil, f.err
	}
	if index >= len(f.results) {
		index = len(f.results) - 1
	}
	return f.results[index], nil
}

func TestStorageMaintenancePassSchedulesExpiryAndDrainsPressure(t *testing.T) {
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	current := storage.Health{QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityCurrent}
	pressure := storage.Health{QuotaState: storage.HealthPressure, FilesystemState: storage.FilesystemCapacityCurrent}
	fullFilesystem := storage.Health{QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityFull}
	for _, test := range []struct {
		name       string
		healths    []storage.Health
		results    []*storage.EvidenceBatchRetentionResult
		wantPrunes int
	}{
		{"routine", []storage.Health{current}, []*storage.EvidenceBatchRetentionResult{{Total: 1}}, 1},
		{"quota pressure", []storage.Health{pressure, pressure, current}, []*storage.EvidenceBatchRetentionResult{{Total: 1}, {Total: 1}}, 2},
		{"filesystem pressure", []storage.Health{fullFilesystem, fullFilesystem}, []*storage.EvidenceBatchRetentionResult{{Total: 1}, {Total: 0}}, 2},
		{"pressure without expired evidence", []storage.Health{pressure}, []*storage.EvidenceBatchRetentionResult{{Total: 0}}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := &fakeStorageMaintenanceStore{healths: test.healths, results: test.results}
			if err := runStorageMaintenancePass(context.Background(), f, now); err != nil {
				t.Fatal(err)
			}
			if f.prunes != test.wantPrunes {
				t.Fatal("unexpected retention passes", f.prunes)
			}
			for _, got := range f.times {
				if !got.Equal(now) {
					t.Fatal("retention clock moved between passes", got)
				}
			}
		})
	}
}

func TestStorageMaintenancePressurePassesRemainBounded(t *testing.T) {
	pressure := storage.Health{QuotaState: storage.HealthAtQuota, FilesystemState: storage.FilesystemCapacityCurrent}
	f := &fakeStorageMaintenanceStore{healths: []storage.Health{pressure}, results: []*storage.EvidenceBatchRetentionResult{{Total: 1}}}
	if err := runStorageMaintenancePass(context.Background(), f, time.Now().UTC()); err != nil || f.prunes != storageMaintenancePressureMaxPasses {
		t.Fatal("pressure loop escaped bound", f.prunes, err)
	}
	f.err = errors.New("injected retention failure")
	f.prunes = 0
	if err := runStorageMaintenancePass(context.Background(), f, time.Now().UTC()); err == nil || f.prunes != 1 {
		t.Fatal("failed maintenance continued", f.prunes, err)
	}
}

func TestServeStartsMixedRetentionAndStopsBeforeStoreClose(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.fixture", Kind: "lan", EnrolledAt: at, Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSensor(ctx, domain.Sensor{ID: "sensor.fixture", ScopeID: "scope.fixture", Kind: "fixture", Ownership: "builtin", RegisteredAt: at, Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	observation := domain.Observation{ID: "obs.expired", ScopeID: "scope.fixture", SensorID: "sensor.fixture", Kind: "fixture", SourceStream: "fixture", SourceKey: "expired", IngestedAt: at, SchemaVersion: 1, Attribution: "fixture", Payload: []byte(`{}`), Retention: domain.RetentionStandard}
	if ok, err := store.InsertObservation(ctx, observation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, storage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE observations SET expires_at_ns=1 WHERE id=?", observation.ID); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "controller-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(runCtx, []string{"--state-dir", dir}, output, output) }()
	finished := false
	defer func() {
		cancel()
		if finished {
			return
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("controller did not stop after maintenance")
		}
	}()
	deadline := time.After(10 * time.Second)
	for {
		var observations, audits int
		err := db.QueryRow("SELECT count(*) FROM observations").Scan(&observations)
		if err == nil {
			err = db.QueryRow("SELECT count(*) FROM storage_events WHERE kind='retention-expired'").Scan(&audits)
		}
		if err == nil && observations == 0 && audits == 1 {
			return
		}
		select {
		case err := <-done:
			finished = true
			t.Fatal("controller stopped before maintenance committed", err)
		case <-deadline:
			t.Fatal("startup maintenance did not prune expired evidence", observations, audits, err)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
