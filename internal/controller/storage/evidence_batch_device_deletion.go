package storage

import (
	"context"
	"database/sql"
	"errors"
)

const evidenceBatchDeviceDeleteMaxCandidates = 1024

// DeleteEvidenceBatchDevice removes a global device and its legacy/batch links.
// Original observations and claims survive. The caller must exclusively own the
// transaction, roll it back on any error, and commit before acknowledging success.
// This reserved-schema primitive does not expose deletion through the runtime UI.
func DeleteEvidenceBatchDevice(ctx context.Context, tx *sql.Tx, device string) (bool, error) {
	return deleteEvidenceBatchDevice(ctx, tx, device, evidenceBatchDeviceDeleteMaxCandidates)
}

func deleteEvidenceBatchDevice(ctx context.Context, tx *sql.Tx, device string, limit int) (bool, error) {
	if tx == nil {
		return false, ErrEvidenceBatchData
	}
	if err := validateQueryID("device", device); err != nil {
		return false, err
	}
	var fk int
	if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return false, err
	}
	if fk != 1 {
		return false, ErrEvidenceBatchData
	}
	// Global device IDs can have evidence in multiple scopes: delete every link,
	// not just the scope that happened to select the device in the UI.
	first := true
	var cursor int64
	for inspected := 0; ; inspected++ {
		var b evidenceBatchCandidate
		var scope string
		err := tx.QueryRowContext(ctx, batchDeviceDeleteCandidateSQL, device, first, cursor).Scan(&b.ID, &b.SourceID, &b.GroupID, &b.Entries, &b.NextExpiry, &b.FirstClaim, &b.LastClaim, &b.LastClaimExpiry, &b.Data, &b.Sensor, &b.Stream, &scope)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return false, err
		}
		if inspected >= limit {
			return false, ErrEvidenceBatchQueryLimit
		}
		records, err := b.records(ctx, tx, scope)
		if err != nil {
			return false, err
		}
		if err := validateEvidenceBatchObservationBounds(ctx, tx, b.ID, records); err != nil {
			return false, err
		}
		for j := range records {
			links := records[j].Links[:0]
			for _, link := range records[j].Links {
				if link.DeviceID != device {
					links = append(links, link)
				}
			}
			clear(records[j].Links[len(links):])
			records[j].Links = links
		}
		if err := rewriteEvidenceBatchRecords(ctx, tx, b.ID, b.SourceID, b.GroupID, records); err != nil {
			return false, err
		}
		first, cursor = false, b.ID
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM devices WHERE id=?", device)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count != 0, err
}

const batchDeviceDeleteCandidateSQL = `SELECT b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,
 b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT),
 CAST(substr(CAST(s.scope_id AS BLOB),1,129) AS TEXT)
 FROM evidence_batch_identity_routes r INDEXED BY evidence_batch_identity_device
 CROSS JOIN evidence_batches b ON b.identity_group=r.group_id
 JOIN evidence_batch_sources s ON s.id=b.source_id
 WHERE r.device_id=? AND (? OR b.id>?) ORDER BY b.id LIMIT 1`
