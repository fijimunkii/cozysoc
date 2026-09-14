package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// evidenceBatchSchema is reserved for the batch adapter. It is deliberately not
// part of migrate: live queries, retention and migration must be integrated first.
const evidenceBatchSchema = `
CREATE TABLE evidence_batch_sources (
 id INTEGER PRIMARY KEY,
 scope_id TEXT NOT NULL REFERENCES network_scopes(id),
 sensor_id TEXT NOT NULL REFERENCES sensors(id),
 stream TEXT NOT NULL,
 UNIQUE(sensor_id, stream)
) STRICT;
CREATE TABLE evidence_batches (
 id INTEGER PRIMARY KEY,
 source_id INTEGER NOT NULL REFERENCES evidence_batch_sources(id),
 identity_group INTEGER NOT NULL REFERENCES evidence_batch_identity_groups(id),
 entries INTEGER NOT NULL CHECK(entries BETWEEN 1 AND 100),
 next_expiry_ns INTEGER NOT NULL,
 first_claim_ns INTEGER NOT NULL DEFAULT 0,
 last_claim_ns INTEGER NOT NULL DEFAULT 0,
 last_claim_expiry_ns INTEGER NOT NULL DEFAULT 0,
 first_observation_ns INTEGER NOT NULL DEFAULT 0,
 last_observation_ns INTEGER NOT NULL DEFAULT 0,
 last_observation_expiry_ns INTEGER NOT NULL DEFAULT 0,
 data BLOB NOT NULL CHECK(length(data) BETWEEN 1 AND 1048576)
) STRICT;
CREATE INDEX evidence_batches_observation_time ON evidence_batches(last_observation_ns DESC,id DESC);
CREATE INDEX evidence_batches_expiry ON evidence_batches(next_expiry_ns, id);
CREATE INDEX evidence_batches_source ON evidence_batches(source_id, identity_group, id DESC);
CREATE INDEX evidence_batches_identity_group ON evidence_batches(identity_group,id);
CREATE TABLE evidence_batch_lookup (
 id BLOB PRIMARY KEY CHECK(length(id) BETWEEN 2 AND 129),
 source_id INTEGER NOT NULL REFERENCES evidence_batch_sources(id),
 source_key BLOB CHECK(source_key IS NULL OR length(source_key) BETWEEN 2 AND 513),
 batch_id INTEGER NOT NULL REFERENCES evidence_batches(id),
 slot INTEGER NOT NULL CHECK(slot BETWEEN 0 AND 99),
 expires_at_ns INTEGER NOT NULL,
 UNIQUE(batch_id, slot)
) STRICT, WITHOUT ROWID;
-- Matching packed source keys already have an entry in the primary-key tree.
-- Only exceptional keys need a second index. Cross-form uniqueness still holds
-- at the database boundary for both inserts and updates.
CREATE UNIQUE INDEX evidence_batch_replay ON evidence_batch_lookup(source_id, source_key)
 WHERE source_key IS NOT NULL;
CREATE TRIGGER evidence_batch_replay_insert BEFORE INSERT ON evidence_batch_lookup
 WHEN (NEW.source_key IS NULL AND EXISTS (
  SELECT 1 FROM evidence_batch_lookup WHERE source_id=NEW.source_id AND source_key=NEW.id
 )) OR (NEW.source_key IS NOT NULL AND EXISTS (
  SELECT 1 FROM evidence_batch_lookup WHERE id=NEW.source_key AND source_id=NEW.source_id AND source_key IS NULL
 ))
 BEGIN SELECT RAISE(ABORT, 'duplicate evidence source key'); END;
CREATE TRIGGER evidence_batch_replay_update BEFORE UPDATE OF id,source_id,source_key ON evidence_batch_lookup
 WHEN (NEW.source_key IS NULL AND EXISTS (
  SELECT 1 FROM evidence_batch_lookup WHERE source_id=NEW.source_id AND source_key=NEW.id AND id<>OLD.id
 )) OR (NEW.source_key IS NOT NULL AND EXISTS (
  SELECT 1 FROM evidence_batch_lookup WHERE id=NEW.source_key AND source_id=NEW.source_id AND source_key IS NULL AND id<>OLD.id
 ))
 BEGIN SELECT RAISE(ABORT, 'duplicate evidence source key'); END;
` + evidenceBatchIdentitySchema

// packedEvidenceKey is reversible, with disjoint tags for full text and a
// canonical 16-byte digest. It never hashes arbitrary identifiers/source keys.
func packedEvidenceKey(value, prefix string) []byte {
	suffix := strings.TrimPrefix(value, prefix)
	if strings.HasPrefix(value, prefix) && len(suffix) == 32 && strings.ToLower(suffix) == suffix {
		if decoded, err := hex.DecodeString(suffix); err == nil {
			return append([]byte{1}, decoded...)
		}
	}
	return append([]byte{0}, []byte(value)...)
}

// validateCompleteBatchBundle checks relations in addition to codec domain
// validation. Read paths repeat this check because stored bytes are untrusted.
func validateCompleteBatchBundle(r EvidenceBatchRecord) error {
	if r.Observation == nil || r.ObservationExpiresAt == nil || !batchTimeFits(*r.ObservationExpiresAt) {
		return ErrEvidenceBatchData
	}
	o := r.Observation
	return validateRetainedBatchBundle(r, o.ScopeID, o.SensorID, o.SourceStream)
}

// appendEvidenceBatch stages one complete record in an exclusively owned SQL
// transaction. The caller MUST roll back on error and acknowledge only after
// commit. Do not begin this transaction on Store.conn, whose autocommit users
// could otherwise have their writes absorbed by this transaction.
// A source-key replay returns false without replacing original evidence/expiry.
// This primitive does not implement identity indexes or quota policy.
func appendEvidenceBatch(ctx context.Context, tx *sql.Tx, r EvidenceBatchRecord) (bool, error) {
	single, err := EncodeEvidenceBatch([]EvidenceBatchRecord{r})
	if err != nil {
		return false, err
	}
	if err := validateCompleteBatchBundle(r); err != nil {
		return false, err
	}
	if err := validateEvidenceBatchClaimTimes([]EvidenceBatchRecord{r}); err != nil {
		return false, err
	}
	o := r.Observation
	// An observation cannot authorize its own scope.
	var sensorScope string
	if err := tx.QueryRowContext(ctx, "SELECT scope_id FROM sensors WHERE id = ?", o.SensorID).Scan(&sensorScope); err != nil {
		return false, err
	}
	if sensorScope != o.ScopeID {
		return false, ErrEvidenceBatchData
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evidence_batch_sources(scope_id,sensor_id,stream) VALUES(?,?,?) ON CONFLICT(sensor_id,stream) DO NOTHING`, o.ScopeID, o.SensorID, o.SourceStream); err != nil {
		return false, err
	}
	var source int64
	var scope string
	if err := tx.QueryRowContext(ctx, `SELECT id,scope_id FROM evidence_batch_sources WHERE sensor_id=? AND stream=?`, o.SensorID, o.SourceStream).Scan(&source, &scope); err != nil {
		return false, err
	}
	if scope != o.ScopeID {
		return false, ErrEvidenceBatchData
	}
	id, key := packedEvidenceKey(o.ID, "obs.dw."), packedEvidenceKey(o.SourceKey, "")
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM evidence_batch_lookup WHERE id=? AND source_id=? AND source_key IS NULL
 UNION ALL SELECT 1 FROM evidence_batch_lookup WHERE source_id=? AND source_key=? LIMIT 1`, key, source, source, key).Scan(&exists)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// Reject an ID collision before rewriting a partial batch.
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM evidence_batch_lookup WHERE id=?`, id).Scan(&exists)
	if err == nil {
		return false, ErrEvidenceBatchData
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err := validateBatchLegacyIdentityIDs(ctx, tx, r); err != nil {
		return false, err
	}
	identityGroup, err := ensureEvidenceBatchIdentityGroup(ctx, tx, source, r)
	if err != nil {
		return false, err
	}
	var batch int64
	var count int
	var nextExpiry int64
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT id,entries,next_expiry_ns,CASE WHEN length(data)<=? THEN data ELSE NULL END FROM evidence_batches WHERE source_id=? AND identity_group=? ORDER BY id DESC LIMIT 1`, EvidenceBatchMaxBytes, source, identityGroup).Scan(&batch, &count, &nextExpiry, &data)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	slot := 0
	combinedRecords := []EvidenceBatchRecord{r}
	if err == nil {
		records, decodeErr := DecodeEvidenceBatch(data)
		if decodeErr != nil {
			return false, decodeErr
		}
		if count != len(records) || nextExpiry != nextEvidenceBatchExpiry(records) {
			return false, ErrEvidenceBatchData
		}
		for _, retained := range records {
			if validateRetainedBatchBundle(retained, scope, o.SensorID, o.SourceStream) != nil {
				return false, ErrEvidenceBatchData
			}
		}
		if err := validateEvidenceBatchBounds(ctx, tx, batch, records); err != nil {
			return false, err
		}
		if err := validateEvidenceBatchLookups(ctx, tx, batch, source, records); err != nil {
			return false, err
		}
		if err := validateEvidenceBatchIdentityGroup(ctx, tx, identityGroup, source, records); err != nil {
			return false, err
		}
		if count < EvidenceBatchMaxRecords {
			combined, encodeErr := EncodeEvidenceBatch(append(records, r))
			if encodeErr == nil {
				data, slot = combined, count
				combinedRecords = append(records, r)
				nextExpiry = nextEvidenceBatchExpiry(append(records, r))
			} else if !errors.Is(encodeErr, ErrEvidenceBatchLimit) {
				return false, encodeErr
			}
		}
	}
	if slot == 0 {
		result, err := tx.ExecContext(ctx, `INSERT INTO evidence_batches(source_id,identity_group,entries,next_expiry_ns,data) VALUES(?,?,1,?,?)`, source, identityGroup, nextEvidenceBatchExpiry([]EvidenceBatchRecord{r}), single)
		if err != nil {
			return false, err
		}
		batch, err = result.LastInsertId()
		if err != nil {
			return false, err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE evidence_batches SET entries=?,next_expiry_ns=?,data=? WHERE id=?`, slot+1, nextExpiry, data, batch); err != nil {
		return false, err
	}
	if err := writeEvidenceBatchBounds(ctx, tx, batch, combinedRecords); err != nil {
		return false, err
	}
	var storedKey any = key
	if bytes.Equal(key, id) {
		storedKey = nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO evidence_batch_lookup(id,source_id,source_key,batch_id,slot,expires_at_ns) VALUES(?,?,?,?,?,?)`, id, source, storedKey, batch, slot, r.ObservationExpiresAt.UnixNano())
	return err == nil, err
}

// readEvidenceBatch reads one original bundle in the caller's snapshot. The
// observation's stored expiry controls availability of this lookup; individual
// claims retain their expiry metadata and are NOT projected as current identity.
func readEvidenceBatch(ctx context.Context, tx *sql.Tx, scope, id string, now time.Time) (EvidenceBatchRecord, error) {
	if validateQueryID("scope", scope) != nil || validateQueryID("observation", id) != nil || !batchTimeFits(now) {
		return EvidenceBatchRecord{}, ErrEvidenceBatchData
	}
	var data, key []byte
	var slot, count int
	var expiry int64
	var sensor, stream string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN length(b.data)<=? THEN b.data ELSE NULL END,b.entries,l.slot,l.expires_at_ns,CASE WHEN length(coalesce(l.source_key,l.id))<=513 THEN coalesce(l.source_key,l.id) ELSE NULL END,s.sensor_id,s.stream
 FROM evidence_batch_lookup l JOIN evidence_batches b ON b.id=l.batch_id AND b.source_id=l.source_id JOIN evidence_batch_sources s ON s.id=l.source_id
 WHERE l.id=? AND s.scope_id=? AND l.expires_at_ns>?`, EvidenceBatchMaxBytes, packedEvidenceKey(id, "obs.dw."), scope, now.UnixNano()).Scan(&data, &count, &slot, &expiry, &key, &sensor, &stream)
	if err != nil {
		return EvidenceBatchRecord{}, err
	}
	records, err := DecodeEvidenceBatch(data)
	if err != nil {
		return EvidenceBatchRecord{}, err
	}
	if count != len(records) || slot < 0 || slot >= len(records) {
		return EvidenceBatchRecord{}, ErrEvidenceBatchData
	}
	r := records[slot]
	if validateCompleteBatchBundle(r) != nil || r.Observation.ID != id || r.Observation.ScopeID != scope || r.Observation.SensorID != sensor || r.Observation.SourceStream != stream || !batchTimeFits(*r.ObservationExpiresAt) || r.ObservationExpiresAt.UnixNano() != expiry || !bytes.Equal(packedEvidenceKey(r.Observation.SourceKey, ""), key) {
		return EvidenceBatchRecord{}, ErrEvidenceBatchData
	}
	return r, nil
}

func batchTimeFits(t time.Time) bool { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }
