package storage

import (
	"container/heap"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func normalizeActivityQuery(q DeviceActivityQuery, now time.Time) (DeviceActivityQuery, error) {
	if err := validateQueryID("scope id", q.ScopeID); err != nil {
		return q, err
	}
	if q.AsOf.IsZero() {
		q.AsOf = now
	} else {
		q.AsOf = q.AsOf.UTC()
	}
	if q.Limit == 0 {
		q.Limit = MaxDeviceActivityItems
	}
	if q.Limit < 1 || q.Limit > MaxDeviceActivityItems {
		return q, fmt.Errorf("device activity limit must be between 1 and %d", MaxDeviceActivityItems)
	}
	if !batchTimeFits(now) || !batchTimeFits(q.AsOf) || !batchTimeFits(q.AsOf.Add(-deviceActivityHistoryWindow)) {
		return q, ErrEvidenceBatchData
	}
	return q, nil
}

// ListDeviceActivity classifies a combined history, never separately classified
// legacy/batch feeds. The caller owns the fixed read snapshot and connection.
func (s *MixedIdentitySnapshot) ListDeviceActivity(ctx context.Context, q DeviceActivityQuery) (DeviceActivityPage, error) {
	if s == nil || s.legacy == nil {
		return DeviceActivityPage{}, ErrEvidenceBatchData
	}
	budget := defaultActivityQueryBudget()
	return readMixedDeviceActivity(ctx, s.legacy.tx, s.legacy.now, q, &budget)
}

const activityCandidateDeviceSQL = `SELECT CAST(substr(CAST(d.id AS BLOB),1,129) AS TEXT),
 CAST(substr(CAST(d.user_label AS BLOB),1,513) AS TEXT),d.created_at_ns,d.retired_at_ns
 FROM devices d WHERE d.id>? AND (d.retired_at_ns IS NULL OR d.retired_at_ns>=?) AND (
 EXISTS(SELECT 1 FROM device_claim_links l JOIN identity_claims c ON c.id=l.claim_id
 JOIN observations o ON o.id=l.evidence_observation_id AND o.id=c.source_observation_id
 WHERE l.device_id=d.id AND o.scope_id=? AND o.kind='device-neighbor-seen'
 AND o.expires_at_ns>? AND c.expires_at_ns>? AND c.observed_at_ns>=? AND c.observed_at_ns<=? AND l.valid_from_ns<=?)
 OR EXISTS(SELECT 1 FROM evidence_batch_identity_routes r INDEXED BY evidence_batch_identity_device
 CROSS JOIN evidence_batches b ON b.identity_group=r.group_id JOIN evidence_batch_sources s ON s.id=b.source_id
 WHERE r.device_id=d.id AND s.scope_id=? AND b.first_claim_ns<=? AND b.last_claim_ns>=? AND b.last_claim_expiry_ns>?))
 ORDER BY d.id LIMIT 1`

const activityCandidateBatchSQL = `SELECT b.id,b.source_id,b.identity_group,b.entries,b.next_expiry_ns,
 b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,
 CAST(substr(CAST(s.sensor_id AS BLOB),1,513) AS TEXT),CAST(substr(CAST(s.stream AS BLOB),1,513) AS TEXT)
 FROM evidence_batch_identity_routes r INDEXED BY evidence_batch_identity_device
 CROSS JOIN evidence_batches b ON b.identity_group=r.group_id JOIN evidence_batch_sources s ON s.id=b.source_id
 WHERE r.device_id=? AND s.scope_id=? AND b.first_claim_ns<=? AND b.last_claim_ns>=? AND b.last_claim_expiry_ns>?
 AND (? OR b.id>?) ORDER BY b.id LIMIT 1`

func readMixedDeviceActivity(ctx context.Context, tx *sql.Tx, now time.Time, q DeviceActivityQuery, budget *activityQueryBudget) (DeviceActivityPage, error) {
	q, err := normalizeActivityQuery(q, now)
	if err != nil {
		return DeviceActivityPage{}, err
	}
	historySince := q.AsOf.Add(-deviceActivityHistoryWindow)
	since := q.AsOf.Add(-DeviceActivityWindow)
	best := activityTopItems{}
	cursor := ""
	for {
		var device domain.Device
		var label sql.NullString
		var created int64
		var retired sql.NullInt64
		err := tx.QueryRowContext(ctx, activityCandidateDeviceSQL, cursor, q.AsOf.UnixNano(), q.ScopeID, now.UnixNano(), now.UnixNano(), historySince.UnixNano(), q.AsOf.UnixNano(), q.AsOf.UnixNano(), q.ScopeID, q.AsOf.UnixNano(), historySince.UnixNano(), now.UnixNano()).Scan(&device.ID, &label, &created, &retired)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return DeviceActivityPage{}, err
		}
		if budget.devices <= 0 {
			return DeviceActivityPage{}, ErrEvidenceBatchQueryLimit
		}
		budget.devices--
		device.UserLabel = label.String
		device.CreatedAt = time.Unix(0, created).UTC()
		if retired.Valid {
			at := time.Unix(0, retired.Int64).UTC()
			device.RetiredAt = &at
		}
		if err := domain.ValidateDevice(device); err != nil {
			return DeviceActivityPage{}, err
		}
		rows := activityDeviceRows{}
		if err := appendLegacyActivityRows(ctx, tx, now, q, device.ID, &rows, budget); err != nil {
			return DeviceActivityPage{}, err
		}
		var batchCursor int64
		first := true
		for {
			var b evidenceBatchCandidate
			err := tx.QueryRowContext(ctx, activityCandidateBatchSQL, device.ID, q.ScopeID, q.AsOf.UnixNano(), historySince.UnixNano(), now.UnixNano(), first, batchCursor).Scan(&b.ID, &b.SourceID, &b.GroupID, &b.Entries, &b.NextExpiry, &b.FirstClaim, &b.LastClaim, &b.LastClaimExpiry, &b.Data, &b.Sensor, &b.Stream)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return DeviceActivityPage{}, err
			}
			if budget.batches <= 0 {
				return DeviceActivityPage{}, ErrEvidenceBatchQueryLimit
			}
			budget.batches--
			records, err := b.records(ctx, tx, q.ScopeID)
			if err != nil {
				return DeviceActivityPage{}, err
			}
			for _, record := range records {
				row, ok, err := batchActivityRow(record, device.ID, now, historySince, q.AsOf)
				if err != nil {
					return DeviceActivityPage{}, err
				}
				if ok {
					if err := rows.add(row, budget); err != nil {
						return DeviceActivityPage{}, err
					}
				}
			}
			first, batchCursor = false, b.ID
		}
		if err := classifyActivityDevice(ctx, device, rows.rows, since, q.Limit+1, &best); err != nil {
			return DeviceActivityPage{}, err
		}
		cursor = device.ID
	}
	sort.Slice(best, func(i, j int) bool { return activityBefore(best[i], best[j]) })
	page := DeviceActivityPage{Since: since, Items: make([]DeviceActivityItem, 0, min(q.Limit, len(best))), Truncated: len(best) > q.Limit}
	page.Items = append(page.Items, best[:min(q.Limit, len(best))]...)
	return page, nil
}

func classifyActivityDevice(ctx context.Context, device domain.Device, rows []activityRawRow, since time.Time, limit int, best *activityTopItems) error {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At != rows[j].At {
			return rows[i].At < rows[j].At
		}
		return rows[i].ID < rows[j].ID
	})
	previous := map[string]string{}
	for index, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		kind := DeviceActivityObserved
		before, hasPrevious := previous[row.Family]
		if index == 0 && row.At == device.CreatedAt.UnixNano() {
			kind = DeviceActivityFirstObserved
		} else if hasPrevious && before != row.Address {
			kind = DeviceActivityAddressChanged
		}
		previous[row.Family] = row.Address
		if row.At < since.UnixNano() || (kind == DeviceActivityObserved && (index != len(rows)-1 || row.At == device.CreatedAt.UnixNano())) {
			continue
		}
		item := DeviceActivityItem{ID: row.ID, Kind: kind, At: time.Unix(0, row.At).UTC(), DeviceID: device.ID, UserLabel: device.UserLabel, AddressFamily: row.Family, Address: row.Address, HardwareAddress: row.Hardware, Source: row.Source}
		if kind == DeviceActivityAddressChanged {
			item.PreviousAddress = before
		}
		if len(*best) < limit {
			heap.Push(best, item)
		} else if activityBefore(item, (*best)[0]) {
			(*best)[0] = item
			heap.Fix(best, 0)
		}
	}
	return nil
}

func activityBefore(a, b DeviceActivityItem) bool {
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.DeviceID < b.DeviceID
}

type activityTopItems []DeviceActivityItem

func (h activityTopItems) Len() int           { return len(h) }
func (h activityTopItems) Less(i, j int) bool { return activityBefore(h[j], h[i]) }
func (h activityTopItems) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *activityTopItems) Push(x any)        { *h = append(*h, x.(DeviceActivityItem)) }
func (h *activityTopItems) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
