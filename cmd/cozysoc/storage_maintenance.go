package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const (
	storageMaintenanceInterval          = 15 * time.Minute
	storageMaintenanceTimeout           = 5 * time.Minute
	storageMaintenanceMaxLegacyRows     = 10000
	storageMaintenanceMaxBatches        = 100
	storageMaintenancePressureMaxPasses = 64
)

type storageMaintenanceStore interface {
	Health(context.Context) (storage.Health, error)
	PruneEvidenceBatchExpired(context.Context, time.Time, int, int) (*storage.EvidenceBatchRetentionResult, error)
}

// startStorageMaintenance completes the first pass before the controller
// accepts requests. Later passes run on the ticker and are joined before the
// parent Store closes. The caller starts it after acquiring the local socket.
func startStorageMaintenance(ctx context.Context, store storageMaintenanceStore, logger *slog.Logger) (func(), error) {
	if store == nil {
		return nil, fmt.Errorf("storage maintenance requires a store")
	}
	if logger == nil {
		logger = slog.Default()
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := func() {
		if runCtx.Err() != nil {
			return
		}
		passCtx, passCancel := context.WithTimeout(runCtx, storageMaintenanceTimeout)
		defer passCancel()
		if err := runStorageMaintenancePass(passCtx, store, time.Now().UTC()); err != nil && runCtx.Err() == nil {
			logger.Warn("storage_maintenance_failed")
		}
	}
	run()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(storageMaintenanceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}, nil
}

// Retention runs once even at healthy capacity. At quota or filesystem pressure,
// additional bounded passes reclaim all currently due evidence they can reach.
// A fixed clock across passes prevents the scheduler from advancing expiry.
func runStorageMaintenancePass(ctx context.Context, store storageMaintenanceStore, now time.Time) error {
	health, err := store.Health(ctx)
	if err != nil {
		return err
	}
	pressure := storageMaintenancePressure(health)
	maxPasses := 1
	if pressure {
		maxPasses = storageMaintenancePressureMaxPasses
	}
	for pass := 0; pass < maxPasses; pass++ {
		result, err := store.PruneEvidenceBatchExpired(ctx, now, storageMaintenanceMaxLegacyRows, storageMaintenanceMaxBatches)
		if err != nil {
			return err
		}
		if result == nil {
			return fmt.Errorf("storage maintenance returned no result")
		}
		if !pressure || result.Total == 0 {
			return nil
		}
		health, err = store.Health(ctx)
		if err != nil {
			return err
		}
		if !storageMaintenancePressure(health) {
			return nil
		}
	}
	return nil
}

func storageMaintenancePressure(health storage.Health) bool {
	return health.QuotaState == storage.HealthPressure || health.QuotaState == storage.HealthAtQuota ||
		health.FilesystemState == storage.FilesystemCapacityPressure || health.FilesystemState == storage.FilesystemCapacityFull
}
