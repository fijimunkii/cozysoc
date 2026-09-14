package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const evidenceBatchMaxPruneBatches = 100

type evidenceBatchPruneCounts struct{ Batches, Observations, Claims, Links int }

// Retained bundles may lose their observation before their claims. The null
// references mirror legacy ON DELETE SET NULL; links require a surviving claim.
func validateRetainedBatchBundle(r EvidenceBatchRecord, scope, sensor, stream string) error {
	if err := validateEvidenceBatchRecord(r); err != nil {
		return err
	}
	observationID := ""
	if r.Observation != nil {
		if r.Observation.ScopeID != scope || r.Observation.SensorID != sensor || r.Observation.SourceStream != stream || !batchTimeFits(r.Observation.IngestedAt) || (r.Observation.SourceTime != nil && !batchTimeFits(*r.Observation.SourceTime)) || !batchTimeFits(*r.ObservationExpiresAt) {
			return ErrEvidenceBatchData
		}
		observationID = r.Observation.ID
	}
	claims := make(map[string]bool, len(r.Claims))
	claimKeys := map[struct {
		kind  domain.ClaimKind
		value string
	}]bool{}
	for _, c := range r.Claims {
		if !batchTimeFits(c.ExpiresAt) || c.Claim.ScopeID != scope || c.Claim.SourceSensorID != sensor || c.Claim.SourceObservationID != observationID || claims[c.Claim.ID] {
			return ErrEvidenceBatchData
		}
		// Legacy UNIQUE(source_observation_id,kind,value) applies only while
		// the source observation exists. NULL sources intentionally permit
		// independently retained claims with the same normalized value.
		if observationID != "" {
			value, err := domain.NormalizeClaimValue(c.Claim.Kind, c.Claim.Value)
			if err != nil {
				return err
			}
			key := struct {
				kind  domain.ClaimKind
				value string
			}{c.Claim.Kind, value}
			if claimKeys[key] {
				return ErrEvidenceBatchData
			}
			claimKeys[key] = true
		}
		claims[c.Claim.ID] = true
	}
	links := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		if !claims[l.ClaimID] || l.EvidenceObservationID != observationID || links[l.ID] {
			return ErrEvidenceBatchData
		}
		links[l.ID] = true
	}
	return nil
}

// All records have been validated and contain an observation or a claim.
func nextEvidenceBatchExpiry(records []EvidenceBatchRecord) int64 {
	var earliest int64
	first := true
	add := func(t time.Time) {
		n := t.UnixNano()
		if first || n < earliest {
			earliest = n
			first = false
		}
	}
	for _, r := range records {
		if r.ObservationExpiresAt != nil {
			add(*r.ObservationExpiresAt)
		}
		for _, c := range r.Claims {
			add(c.ExpiresAt)
		}
	}
	return earliest
}

func retainedEvidenceAfter(r EvidenceBatchRecord, now time.Time) (EvidenceBatchRecord, evidenceBatchPruneCounts) {
	var counts evidenceBatchPruneCounts
	result := r
	result.Claims = nil
	result.Links = nil
	if r.Observation != nil && !r.ObservationExpiresAt.After(now) {
		result.Observation = nil
		result.ObservationExpiresAt = nil
		counts.Observations++
	}
	kept := make(map[string]bool, len(r.Claims))
	for _, c := range r.Claims {
		if !c.ExpiresAt.After(now) {
			counts.Claims++
			continue
		}
		if result.Observation == nil {
			c.Claim.SourceObservationID = ""
		}
		result.Claims = append(result.Claims, c)
		kept[c.Claim.ID] = true
	}
	for _, l := range r.Links {
		if !kept[l.ClaimID] {
			counts.Links++
			continue
		}
		if result.Observation == nil {
			l.EvidenceObservationID = ""
		}
		result.Links = append(result.Links, l)
	}
	// Preserve nil versus empty slices when retention did not change them.
	if len(r.Claims) == 0 {
		result.Claims = r.Claims
	}
	if len(r.Links) == 0 {
		result.Links = r.Links
	}
	return result, counts
}

// pruneEvidenceBatches stages bounded retention work in the caller's exclusive
// transaction. Counts become authoritative only after commit; any error requires
// rollback, including cancellation or quota failure during a rewrite. This does
// not VACUUM, extend expiry, publish audit events, or change live Store pruning.
func pruneEvidenceBatches(ctx context.Context, tx *sql.Tx, now time.Time, maxBatches int) (evidenceBatchPruneCounts, error) {
	var total evidenceBatchPruneCounts
	if !batchTimeFits(now) || maxBatches < 1 || maxBatches > evidenceBatchMaxPruneBatches {
		return total, ErrEvidenceBatchData
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM evidence_batches WHERE next_expiry_ns<=? ORDER BY next_expiry_ns,id LIMIT ?`, now.UnixNano(), maxBatches)
	if err != nil {
		return total, err
	}
	ids := make([]int64, 0, maxBatches)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return total, err
	}
	for _, id := range ids {
		counts, err := pruneEvidenceBatch(ctx, tx, id, now)
		if err != nil {
			return evidenceBatchPruneCounts{}, err
		}
		total.Batches++
		total.Observations += counts.Observations
		total.Claims += counts.Claims
		total.Links += counts.Links
	}
	return total, nil
}

func pruneEvidenceBatch(ctx context.Context, tx *sql.Tx, id int64, now time.Time) (evidenceBatchPruneCounts, error) {
	var counts evidenceBatchPruneCounts
	var source, expiry, identityGroup int64
	var count int
	var data []byte
	var scope, sensor, stream string
	err := tx.QueryRowContext(ctx, `SELECT b.source_id,b.identity_group,b.entries,b.next_expiry_ns,CASE WHEN length(b.data)<=? THEN b.data ELSE NULL END,s.scope_id,s.sensor_id,s.stream FROM evidence_batches b JOIN evidence_batch_sources s ON s.id=b.source_id WHERE b.id=?`, EvidenceBatchMaxBytes, id).Scan(&source, &identityGroup, &count, &expiry, &data, &scope, &sensor, &stream)
	if err != nil {
		return counts, err
	}
	records, err := DecodeEvidenceBatch(data)
	if err != nil {
		return counts, err
	}
	if count != len(records) || expiry != nextEvidenceBatchExpiry(records) {
		return counts, ErrEvidenceBatchData
	}
	for _, r := range records {
		if err := validateRetainedBatchBundle(r, scope, sensor, stream); err != nil {
			return counts, err
		}
	}
	if err := validateEvidenceBatchBounds(ctx, tx, id, records); err != nil {
		return counts, err
	}
	if err := validateEvidenceBatchIdentityGroup(ctx, tx, identityGroup, source, records); err != nil {
		return counts, err
	}
	if err := validateEvidenceBatchLookups(ctx, tx, id, source, records); err != nil {
		return counts, err
	}
	retained := make([]EvidenceBatchRecord, 0, len(records))
	for _, r := range records {
		next, removed := retainedEvidenceAfter(r, now)
		counts.Observations += removed.Observations
		counts.Claims += removed.Claims
		counts.Links += removed.Links
		if next.Observation != nil || len(next.Claims) > 0 {
			retained = append(retained, next)
		}
	}
	// Removing observations can disable derived-ID packing, increasing encoded
	// size. Repartition if needed; never drop retained evidence to fit a batch.
	groups, err := partitionIdentityEvidence(retained)
	if err != nil {
		return counts, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM evidence_batch_lookup WHERE batch_id=?`, id); err != nil {
		return counts, err
	}
	if len(groups) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM evidence_batches WHERE id=?`, id)
		if err != nil {
			return counts, err
		}
		return counts, removeUnusedEvidenceBatchIdentityGroup(ctx, tx, identityGroup)
	}
	for i, group := range groups {
		data, err := EncodeEvidenceBatch(group)
		if err != nil {
			return counts, err
		}
		newIdentityGroup, err := ensureEvidenceBatchIdentityGroup(ctx, tx, source, group[0])
		if err != nil {
			return counts, err
		}
		batchID := id
		if i == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE evidence_batches SET identity_group=?,entries=?,next_expiry_ns=?,data=? WHERE id=?`, newIdentityGroup, len(group), nextEvidenceBatchExpiry(group), data, id)
		} else {
			var result sql.Result
			result, err = tx.ExecContext(ctx, `INSERT INTO evidence_batches(source_id,identity_group,entries,next_expiry_ns,data) VALUES(?,?,?,?,?)`, source, newIdentityGroup, len(group), nextEvidenceBatchExpiry(group), data)
			if err == nil {
				batchID, err = result.LastInsertId()
			}
		}
		if err != nil {
			return counts, err
		}
		if err := writeEvidenceBatchBounds(ctx, tx, batchID, group); err != nil {
			return counts, err
		}
		for slot, r := range group {
			if r.Observation == nil {
				continue
			}
			id, key := packedEvidenceKey(r.Observation.ID, "obs.dw."), packedEvidenceKey(r.Observation.SourceKey, "")
			var storedKey any = key
			if bytes.Equal(id, key) {
				storedKey = nil
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_batch_lookup(id,source_id,source_key,batch_id,slot,expires_at_ns) VALUES(?,?,?,?,?,?)`, id, source, storedKey, batchID, slot, r.ObservationExpiresAt.UnixNano()); err != nil {
				return counts, err
			}
		}
	}
	return counts, removeUnusedEvidenceBatchIdentityGroup(ctx, tx, identityGroup)
}

func partitionRetainedEvidence(records []EvidenceBatchRecord) ([][]EvidenceBatchRecord, error) {
	if len(records) == 0 {
		return nil, nil
	}
	// Normal pruning needs one encode. Partition only when materialized IDs push
	// the retained representation over the limit.
	if _, err := EncodeEvidenceBatch(records); err == nil {
		return [][]EvidenceBatchRecord{records}, nil
	} else if !errors.Is(err, ErrEvidenceBatchLimit) {
		return nil, err
	}
	groups := make([][]EvidenceBatchRecord, 0, 2)
	start := 0
	for end := 1; end <= len(records); end++ {
		if _, err := EncodeEvidenceBatch(records[start:end]); err != nil {
			if !errors.Is(err, ErrEvidenceBatchLimit) || end-start == 1 {
				return nil, err
			}
			groups = append(groups, records[start:end-1])
			start = end - 1
			if _, err := EncodeEvidenceBatch(records[start:end]); err != nil {
				return nil, err
			}
		}
	}
	return append(groups, records[start:]), nil
}

// Verify before rebuilding, rather than silently repairing missing, misplaced or
// conflicting lookup rows. Bound rows as well as payload bytes on corrupt input.
func validateEvidenceBatchLookups(ctx context.Context, tx *sql.Tx, batch, source int64, records []EvidenceBatchRecord) error {
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN length(id)<=129 THEN id ELSE NULL END,source_id,CASE WHEN length(coalesce(source_key,id))<=513 THEN coalesce(source_key,id) ELSE NULL END,slot,expires_at_ns FROM evidence_batch_lookup WHERE batch_id=? LIMIT ?`, batch, EvidenceBatchMaxRecords+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[int]bool, len(records))
	for rows.Next() {
		var id, key []byte
		var rowSource, expiry int64
		var slot int
		if err := rows.Scan(&id, &rowSource, &key, &slot, &expiry); err != nil {
			return err
		}
		if slot < 0 || slot >= len(records) || seen[slot] || rowSource != source {
			return ErrEvidenceBatchData
		}
		r := records[slot]
		if r.Observation == nil || !bytes.Equal(id, packedEvidenceKey(r.Observation.ID, "obs.dw.")) || !bytes.Equal(key, packedEvidenceKey(r.Observation.SourceKey, "")) || expiry != r.ObservationExpiresAt.UnixNano() {
			return ErrEvidenceBatchData
		}
		seen[slot] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for slot, r := range records {
		if (r.Observation != nil) != seen[slot] {
			return ErrEvidenceBatchData
		}
	}
	return nil
}
