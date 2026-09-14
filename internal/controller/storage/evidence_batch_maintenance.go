package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EvidenceBatchRetentionResult counts committed expiry across both formats.
// ExpiredRows and Total count observations, claims and other legacy rows, not
// cascaded links or internal batch/index rows. The Batch fields are a breakdown,
// not additional rows to add to Total. BatchLinks counts cascaded batch links.
type EvidenceBatchRetentionResult struct {
	ExpiredRows       map[string]int64 `json:"expired_rows"`
	Total             int64            `json:"total"`
	BatchesProcessed  int              `json:"batches_processed"`
	BatchObservations int              `json:"batch_observations"`
	BatchClaims       int              `json:"batch_claims"`
	BatchLinks        int              `json:"batch_links"`
}

// PruneEvidenceBatchExpired commits one bounded mixed-format retention pass and
// its storage event atomically on a private, quota-configured connection. The
// reserved schema must already exist; this does not activate live maintenance.
// maxRows bounds each legacy table, while maxBatches bounds decoded batches.
func (s *Store) PruneEvidenceBatchExpired(ctx context.Context, now time.Time, maxRows, maxBatches int) (*EvidenceBatchRetentionResult, error) {
	if err := validateMixedRetentionLimits(now, maxRows, maxBatches); err != nil {
		return nil, err
	}
	writer, err := openEvidenceBatchWriter(ctx, s)
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	return writer.pruneEvidenceBatchExpired(ctx, now, maxRows, maxBatches)
}

func validateMixedRetentionLimits(now time.Time, maxRows, maxBatches int) error {
	if !batchTimeFits(now) || maxRows < 1 || maxRows > 10000 || maxBatches < 1 || maxBatches > evidenceBatchMaxPruneBatches {
		return fmt.Errorf("mixed retention requires a valid time, 1–10000 legacy rows and 1–100 batches")
	}
	return nil
}

// Only an exclusively owned writer may call this method.
func (s *Store) pruneEvidenceBatchExpired(ctx context.Context, now time.Time, maxRows, maxBatches int) (*EvidenceBatchRetentionResult, error) {
	if err := validateMixedRetentionLimits(now, maxRows, maxBatches); err != nil {
		return nil, err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireEvidenceBatchForeignKeys(ctx, tx); err != nil {
		return nil, err
	}
	rows, total, err := pruneLegacyExpired(ctx, tx, now, maxRows)
	if err != nil {
		return nil, err
	}
	batch, err := pruneEvidenceBatches(ctx, tx, now, maxBatches)
	if err != nil {
		return nil, err
	}
	for table, count := range map[string]int{"observations": batch.Observations, "identity_claims": batch.Claims} {
		if count > 0 {
			rows[table] += int64(count)
			total += int64(count)
		}
	}
	result := &EvidenceBatchRetentionResult{ExpiredRows: rows, Total: total, BatchesProcessed: batch.Batches, BatchObservations: batch.Observations, BatchClaims: batch.Claims, BatchLinks: batch.Links}
	if total > 0 {
		details, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if err := insertStorageEvent(ctx, tx, "retention-expired", now, details, s.expiry); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, wrapWrite("commit mixed retention", err)
	}
	return result, nil
}
