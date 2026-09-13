package devicewatch

import (
	"context"
	"database/sql"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

// RepairLegacyObservation handles ErrEvidenceBatchLegacyReplay with the original
// stored evidence. The owner acknowledges only after committing this transaction.
// False means the original is absent/expired; no old evidence is resurrected.
func RepairLegacyObservation(ctx context.Context, tx *sql.Tx, limits storage.Limits, retry domain.Observation) (bool, error) {
	store, retained, err := storage.NewLegacyRepairStore(ctx, tx, limits, retry)
	if err != nil || !retained {
		return false, err
	}
	reconciler, err := NewReconciler(store)
	if err != nil {
		return false, err
	}
	if _, err := reconciler.ReconcileObservation(ctx, store.Observation()); err != nil {
		return false, err
	}
	return true, nil
}
