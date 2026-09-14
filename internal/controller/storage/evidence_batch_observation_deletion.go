package storage

import (
	"context"
	"database/sql"
	"errors"
)

// DeleteEvidenceBatchObservation removes one scoped original observation in the
// caller's exclusively owned transaction. Claims and links survive with cleared
// observation references, matching legacy ON DELETE SET NULL. Roll back on any
// error and commit before acknowledging success. Expiry does not prevent deletion.
func DeleteEvidenceBatchObservation(ctx context.Context, tx *sql.Tx, scope, id string) (bool, error) {
	if tx == nil {
		return false, ErrEvidenceBatchData
	}
	if err := validateQueryID("scope", scope); err != nil {
		return false, err
	}
	if err := validateQueryID("observation", id); err != nil {
		return false, err
	}
	if err := requireEvidenceBatchForeignKeys(ctx, tx); err != nil {
		return false, err
	}
	var legacyScope string
	err := tx.QueryRowContext(ctx, "SELECT CAST(substr(CAST(scope_id AS BLOB),1,129) AS TEXT) FROM observations WHERE id=?", id).Scan(&legacyScope)
	legacy := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var batch, source int64
	var slot int
	err = tx.QueryRowContext(ctx, "SELECT batch_id,source_id,slot FROM evidence_batch_lookup WHERE id=?", packedEvidenceKey(id, "obs.dw.")).Scan(&batch, &source, &slot)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if found && legacy {
		return false, ErrEvidenceBatchData
	}
	if legacy {
		if err := validateQueryID("stored scope", legacyScope); err != nil {
			return false, err
		}
		if legacyScope != scope {
			return false, nil
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM observations WHERE id=? AND scope_id=?", id, scope)
		if err != nil {
			return false, err
		}
		count, err := result.RowsAffected()
		return count != 0, err
	}
	if !found {
		return false, nil
	}
	var b evidenceBatchCandidate
	var storedScope string
	err = tx.QueryRowContext(ctx, observationDeleteBatchSQL, batch).Scan(&b.ID, &b.SourceID, &b.GroupID, &b.Entries, &b.NextExpiry, &b.FirstClaim, &b.LastClaim, &b.LastClaimExpiry, &b.Data, &b.Sensor, &b.Stream, &storedScope)
	if err != nil {
		return false, err
	}
	if b.SourceID != source {
		return false, ErrEvidenceBatchData
	}
	if err := validateQueryID("stored scope", storedScope); err != nil {
		return false, err
	}
	if storedScope != scope {
		return false, nil
	}
	records, err := b.records(ctx, tx, scope)
	if err != nil {
		return false, err
	}
	if err := validateEvidenceBatchObservationBounds(ctx, tx, b.ID, records); err != nil {
		return false, err
	}
	if slot < 0 || slot >= len(records) || records[slot].Observation == nil || records[slot].Observation.ID != id {
		return false, ErrEvidenceBatchData
	}
	records[slot] = withoutBatchObservation(records[slot])
	if len(records[slot].Claims) == 0 {
		// No evidence remains in this record. Rebuild lookup slots for other records.
		records = append(records[:slot], records[slot+1:]...)
	}
	if err := rewriteEvidenceBatchRecords(ctx, tx, b.ID, b.SourceID, b.GroupID, records); err != nil {
		return false, err
	}
	return true, nil
}

func requireEvidenceBatchForeignKeys(ctx context.Context, tx *sql.Tx) error {
	var fk int
	if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return err
	}
	if fk != 1 {
		return ErrEvidenceBatchData
	}
	return nil
}

func withoutBatchObservation(r EvidenceBatchRecord) EvidenceBatchRecord {
	r.Observation = nil
	r.ObservationExpiresAt = nil
	r.Claims = append([]RetainedIdentityClaim(nil), r.Claims...)
	r.Links = append(r.Links[:0:0], r.Links...)
	for j := range r.Claims {
		r.Claims[j].Claim.SourceObservationID = ""
	}
	for j := range r.Links {
		r.Links[j].EvidenceObservationID = ""
	}
	return r
}

const observationDeleteBatchSQL = `SELECT b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,
 b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT),
 CAST(substr(CAST(s.scope_id AS BLOB),1,129) AS TEXT)
 FROM evidence_batches b JOIN evidence_batch_sources s ON s.id=b.source_id WHERE b.id=?`
