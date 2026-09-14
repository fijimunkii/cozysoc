package storage

import (
	"context"
	"database/sql"
)

// Ingestion bounds select history candidates; original records decide ordering
// and retention. Claim times and source event times are independent of ingestion.
func evidenceBatchObservationBounds(records []EvidenceBatchRecord) (first, last, expiry int64, err error) {
	seen := false
	for _, r := range records {
		if r.Observation == nil {
			continue
		}
		if !batchTimeFits(r.Observation.IngestedAt) || r.ObservationExpiresAt == nil || !batchTimeFits(*r.ObservationExpiresAt) {
			return 0, 0, 0, ErrEvidenceBatchData
		}
		at, ex := r.Observation.IngestedAt.UnixNano(), r.ObservationExpiresAt.UnixNano()
		if !seen || at < first {
			first = at
		}
		if !seen || at > last {
			last = at
		}
		if !seen || ex > expiry {
			expiry = ex
		}
		seen = true
	}
	return
}
func validateEvidenceBatchObservationBounds(ctx context.Context, tx *sql.Tx, batch int64, records []EvidenceBatchRecord) error {
	var first, last, expiry int64
	if err := tx.QueryRowContext(ctx, `SELECT first_observation_ns,last_observation_ns,last_observation_expiry_ns FROM evidence_batches WHERE id=?`, batch).Scan(&first, &last, &expiry); err != nil {
		return err
	}
	f, l, e, err := evidenceBatchObservationBounds(records)
	if err != nil {
		return err
	}
	if first != f || last != l || expiry != e {
		return ErrEvidenceBatchData
	}
	return nil
}
