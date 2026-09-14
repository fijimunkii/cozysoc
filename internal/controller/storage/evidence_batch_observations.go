package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const evidenceBatchObservationMaxCandidates = 1024
const observationQueryMaxBytes = 16 << 20

// ListObservations merges original observations in the caller-owned snapshot.
// It never uses claim time or source event time as an ingestion cursor.
func (s *MixedIdentitySnapshot) ListObservations(ctx context.Context, query ObservationQuery) (ObservationPage, error) {
	if s == nil || s.legacy == nil {
		return ObservationPage{}, ErrEvidenceBatchData
	}
	now := s.legacy.now
	q, err := normalizeObservationQuery(query, now)
	if err != nil {
		return ObservationPage{}, err
	}
	if !batchTimeFits(now) || !batchTimeFits(q.Since) || !batchTimeFits(q.Until) || (q.Before != nil && !batchTimeFits(q.Before.IngestedAt)) {
		return ObservationPage{}, ErrEvidenceBatchData
	}
	items, err := listLegacyObservations(ctx, s.legacy.tx, now, q, true)
	if err != nil {
		return ObservationPage{}, err
	}
	seen := make(map[string]bool, len(items))
	for _, o := range items {
		seen[o.ID] = true
	}
	upper := q.Until
	if q.Before != nil && q.Before.IngestedAt.Before(upper) {
		upper = q.Before.IngestedAt
	}
	cursorTime, cursorBatch := int64(math.MaxInt64), int64(0)
	for inspected := 0; ; inspected++ {
		var b evidenceBatchCandidate
		var first, last, expiry int64
		err := s.legacy.tx.QueryRowContext(ctx, batchObservationCandidateSQL, q.Since.UnixNano(), cursorTime, upper.UnixNano(), now.UnixNano(), q.ScopeID, q.SensorID, q.SensorID, inspected == 0, cursorTime, cursorTime, cursorBatch).Scan(&b.ID, &b.SourceID, &b.GroupID, &b.Entries, &b.NextExpiry, &b.FirstClaim, &b.LastClaim, &b.LastClaimExpiry, &b.Data, &b.Sensor, &b.Stream, &first, &last, &expiry)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return ObservationPage{}, err
		}
		if inspected >= evidenceBatchObservationMaxCandidates {
			return ObservationPage{}, ErrEvidenceBatchQueryLimit
		}
		records, err := b.records(ctx, s.legacy.tx, q.ScopeID)
		if err != nil {
			return ObservationPage{}, err
		}
		f, l, e, err := evidenceBatchObservationBounds(records)
		if err != nil {
			return ObservationPage{}, err
		}
		if f != first || l != last || e != expiry {
			return ObservationPage{}, ErrEvidenceBatchData
		}
		// Validate the selected batch before trusting its upper bound. Equal-time
		// batches must all contribute: original ID, not batch ID, orders ties.
		if len(items) > q.Limit && last < items[q.Limit].IngestedAt.UnixNano() {
			break
		}
		for _, r := range records {
			if r.Observation == nil || !r.ObservationExpiresAt.After(now) {
				continue
			}
			o := *r.Observation
			if o.IngestedAt.Before(q.Since) || o.IngestedAt.After(q.Until) || (q.Kind != "" && o.Kind != q.Kind) {
				continue
			}
			if q.Before != nil && (o.IngestedAt.After(q.Before.IngestedAt) || (o.IngestedAt.Equal(q.Before.IngestedAt) && o.ID <= q.Before.ID)) {
				continue
			}
			if seen[o.ID] {
				return ObservationPage{}, ErrEvidenceBatchData
			}
			seen[o.ID] = true
			o.IngestedAt = o.IngestedAt.UTC()
			o.SourceTime = queryUTCTime(o.SourceTime)
			items = append(items, o)
		}
		sort.Slice(items, func(i, j int) bool { return observationBefore(items[i], items[j]) })
		if len(items) > q.Limit+1 {
			clear(items[q.Limit+1:])
			items = items[:q.Limit+1]
		}
		bytes := 0
		for _, o := range items {
			bytes += observationProjectionBytes(o)
		}
		if bytes > observationQueryMaxBytes {
			return ObservationPage{}, ErrEvidenceBatchQueryLimit
		}
		cursorTime, cursorBatch = last, b.ID
	}
	return observationPage(items, q.Limit), nil
}

func observationBefore(a, b domain.Observation) bool {
	if !a.IngestedAt.Equal(b.IngestedAt) {
		return a.IngestedAt.After(b.IngestedAt)
	}
	return a.ID < b.ID
}
func observationProjectionBytes(o domain.Observation) int {
	return 256 + len(o.ID) + len(o.ScopeID) + len(o.SensorID) + len(o.Kind) + len(o.SourceStream) + len(o.SourceKey) + len(o.SourceEventID) + len(o.Attribution) + len(o.Payload) + len(o.Retention)
}

const batchObservationCandidateSQL = `SELECT b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,
 b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT),
 b.first_observation_ns,b.last_observation_ns,b.last_observation_expiry_ns
 FROM evidence_batches b INDEXED BY evidence_batches_observation_time
 JOIN evidence_batch_sources s ON s.id=b.source_id
 WHERE b.last_observation_ns>=? AND b.last_observation_ns<=? AND b.first_observation_ns<=? AND b.last_observation_expiry_ns>?
 AND s.scope_id=? AND (?='' OR s.sensor_id=?)
 AND (? OR b.last_observation_ns<? OR (b.last_observation_ns=? AND b.id<?))
 ORDER BY b.last_observation_ns DESC,b.id DESC LIMIT 1`
