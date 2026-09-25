package storage

import (
	"context"
	"database/sql"
	"errors"
)

// Covers the nominal 30-day, 100-device reference workload (~43,200 full
// batches) while keeping a finite bound until claim-ID indexing is decided.
const evidenceBatchClaimDeleteMaxCandidates = 65536

// DeleteEvidenceBatchClaim removes every claim with id in scope and its links
// from both storage formats in the caller's exclusively owned transaction.
// Original observations and unrelated evidence survive. The owner must roll
// back on any error and commit before acknowledging success.
func DeleteEvidenceBatchClaim(ctx context.Context, tx *sql.Tx, scope, id string) (bool, error) {
	return deleteEvidenceBatchClaim(ctx, tx, scope, id, evidenceBatchClaimDeleteMaxCandidates)
}

func deleteEvidenceBatchClaim(ctx context.Context, tx *sql.Tx, scope, id string, limit int) (bool, error) {
	if tx == nil {
		return false, ErrEvidenceBatchData
	}
	if err := validateQueryID("scope", scope); err != nil {
		return false, err
	}
	if err := validateQueryID("claim", id); err != nil {
		return false, err
	}
	if err := requireEvidenceBatchForeignKeys(ctx, tx); err != nil {
		return false, err
	}

	var legacyScope string
	err := tx.QueryRowContext(ctx, "SELECT CAST(substr(CAST(scope_id AS BLOB),1,129) AS TEXT) FROM identity_claims WHERE id=?", id).Scan(&legacyScope)
	legacy := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if legacy {
		if err := validateQueryID("stored scope", legacyScope); err != nil {
			return false, err
		}
		legacy = legacyScope == scope
	}

	// Freeze the original upper bound. Rewriting a batch may repartition it and
	// allocate later IDs; those new batches already exclude the deleted claim.
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, claimDeleteMaxBatchSQL).Scan(&last); err != nil {
		return false, err
	}
	found := legacy
	if last.Valid {
		var cursor int64
		for inspected := 0; ; inspected++ {
			var batchID int64
			var storedScope string
			query := claimDeleteNextCandidateSQL
			args := []any{last.Int64, cursor}
			if inspected == 0 {
				query = claimDeleteFirstCandidateSQL
				args = args[:1]
			}
			err := tx.QueryRowContext(ctx, query, args...).Scan(&batchID, &storedScope)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return false, err
			}
			if inspected >= limit {
				return false, ErrEvidenceBatchQueryLimit
			}
			if err := validateQueryID("stored scope", storedScope); err != nil {
				return false, err
			}
			if storedScope != scope {
				cursor = batchID
				continue
			}
			var b evidenceBatchCandidate
			err = tx.QueryRowContext(ctx, observationDeleteBatchSQL, batchID).Scan(&b.ID, &b.SourceID, &b.GroupID, &b.Entries, &b.NextExpiry, &b.FirstClaim, &b.LastClaim, &b.LastClaimExpiry, &b.Data, &b.Sensor, &b.Stream, &storedScope)
			if err != nil {
				return false, err
			}
			if b.ID != batchID || storedScope != scope {
				return false, ErrEvidenceBatchData
			}
			records, err := b.records(ctx, tx, scope)
			if err != nil {
				return false, err
			}
			if err := validateEvidenceBatchObservationBounds(ctx, tx, b.ID, records); err != nil {
				return false, err
			}

			changed := false
			retained := make([]EvidenceBatchRecord, 0, len(records))
			for _, record := range records {
				claims := record.Claims[:0]
				removed := false
				for _, claim := range record.Claims {
					if claim.Claim.ID == id {
						removed = true
						changed = true
						found = true
						continue
					}
					claims = append(claims, claim)
				}
				clear(record.Claims[len(claims):])
				record.Claims = claims
				if removed {
					links := record.Links[:0]
					for _, link := range record.Links {
						if link.ClaimID != id {
							links = append(links, link)
						}
					}
					clear(record.Links[len(links):])
					record.Links = links
				}
				if record.Observation != nil || len(record.Claims) != 0 {
					retained = append(retained, record)
				}
			}
			if changed {
				if err := rewriteEvidenceBatchRecords(ctx, tx, b.ID, b.SourceID, b.GroupID, retained); err != nil {
					return false, err
				}
			}
			cursor = batchID
		}
	}

	if legacy {
		result, err := tx.ExecContext(ctx, "DELETE FROM identity_claims WHERE id=? AND scope_id=?", id, scope)
		if err != nil {
			return false, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if count != 1 {
			return false, ErrEvidenceBatchData
		}
	}
	return found, nil
}

const claimDeleteMaxBatchSQL = `SELECT MAX(id) FROM evidence_batches`

const claimDeleteCandidateSelectSQL = `SELECT b.id,CAST(substr(CAST(s.scope_id AS BLOB),1,129) AS TEXT)
 FROM evidence_batches b LEFT JOIN evidence_batch_sources s ON s.id=b.source_id`

const claimDeleteFirstCandidateSQL = claimDeleteCandidateSelectSQL + ` WHERE b.id<=? ORDER BY b.id LIMIT 1`
const claimDeleteNextCandidateSQL = claimDeleteCandidateSelectSQL + ` WHERE b.id<=? AND b.id>? ORDER BY b.id LIMIT 1`
