package storage

import (
	"context"
	"database/sql"
	"errors"
)

// A new bundle cannot reuse primary IDs already owned by legacy identity rows.
// Probe by indexed primary key, including expired-but-unpruned rows. Observation
// replay is resolved before this check, so replay never reassigns identity IDs.
// This is not the still-required cross-batch/global legacy-writer constraint.
func validateBatchLegacyIdentityIDs(ctx context.Context, tx *sql.Tx, r EvidenceBatchRecord) error {
	for _, claim := range r.Claims {
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM identity_claims WHERE id=?", claim.Claim.ID).Scan(&exists)
		if err == nil {
			return ErrEvidenceBatchData
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	for _, link := range r.Links {
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM device_claim_links WHERE id=?", link.ID).Scan(&exists)
		if err == nil {
			return ErrEvidenceBatchData
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}
