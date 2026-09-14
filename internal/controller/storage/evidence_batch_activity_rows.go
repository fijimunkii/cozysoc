package storage

import (
	"context"
	"database/sql"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	activityMaxDeviceRows  = 32768
	activityMaxDeviceBytes = 16 << 20
)

type activityRawRow struct {
	ID                        string
	At                        int64
	Hardware, Family, Address string
	Source                    DeviceActivitySource
}

type activityQueryBudget struct{ devices, batches, rows int }

func defaultActivityQueryBudget() activityQueryBudget {
	return activityQueryBudget{devices: 1024, batches: 32768, rows: 2 << 20}
}

type activityDeviceRows struct {
	rows  []activityRawRow
	bytes int
	ids   map[string]bool
}

func (r *activityDeviceRows) add(row activityRawRow, budget *activityQueryBudget) error {
	if budget.rows <= 0 || len(r.rows) >= activityMaxDeviceRows {
		return ErrEvidenceBatchQueryLimit
	}
	if err := validateQueryID("activity observation", row.ID); err != nil {
		return err
	}
	if err := validateQueryID("activity sensor", row.Source.SensorID); err != nil {
		return err
	}
	if len(row.Source.SourceStream) > 128 || len(row.Source.Attribution) > 256 {
		return ErrEvidenceBatchData
	}
	if row.Source.Kind != "device-neighbor-seen" || (row.Family != "ipv4" && row.Family != "ipv6") {
		return ErrEvidenceBatchData
	}
	if _, err := domain.NormalizeClaimValue(domain.ClaimMAC, row.Hardware); err != nil {
		return err
	}
	// Legacy MAX(address) and MAX(address_family) are independent aggregates.
	if _, err := netip.ParseAddr(row.Address); err != nil {
		return err
	}
	size := 256
	for _, text := range []string{row.ID, row.Hardware, row.Family, row.Address, row.Source.SensorID, row.Source.Kind, row.Source.SourceStream, row.Source.Attribution} {
		if text == "" || len(text) > 512 || strings.TrimSpace(text) != text {
			return ErrEvidenceBatchData
		}
		for _, c := range text {
			if unicode.IsControl(c) {
				return ErrEvidenceBatchData
			}
		}
		size += len(text)
	}
	if r.bytes+size > activityMaxDeviceBytes {
		return ErrEvidenceBatchQueryLimit
	}
	if r.ids == nil {
		r.ids = map[string]bool{}
	}
	if r.ids[row.ID] {
		return ErrEvidenceBatchData
	}
	r.ids[row.ID] = true
	r.rows = append(r.rows, row)
	r.bytes += size
	budget.rows--
	return nil
}

func appendLegacyActivityRows(ctx context.Context, tx *sql.Tx, now time.Time, q DeviceActivityQuery, device string, out *activityDeviceRows, budget *activityQueryBudget) error {
	// Bound materialized text before it reaches Go, including corrupt stored data.
	text := func(column string) string { return "CAST(substr(CAST(" + column + " AS BLOB),1,513) AS TEXT)" }
	columns := []string{text("observation_id"), "observed_at_ns", text("hardware_address"), text("address_family"), text("address"), text("sensor_id"), text("source_kind"), text("source_stream"), "ingested_at_ns", text("attribution")}
	rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM ("+legacyActivityRawSQL(true)+") ORDER BY observed_at_ns,observation_id LIMIT ?", q.ScopeID, now.UnixNano(), now.UnixNano(), q.AsOf.Add(-deviceActivityHistoryWindow).UnixNano(), q.AsOf.UnixNano(), q.AsOf.UnixNano(), q.AsOf.UnixNano(), device, activityMaxDeviceRows+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row activityRawRow
		var ingested int64
		if err := rows.Scan(&row.ID, &row.At, &row.Hardware, &row.Family, &row.Address, &row.Source.SensorID, &row.Source.Kind, &row.Source.SourceStream, &ingested, &row.Source.Attribution); err != nil {
			return err
		}
		row.Source.ObservationID = row.ID
		row.Source.IngestedAt = time.Unix(0, ingested).UTC()
		if err := out.add(row, budget); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Project the same per-observation/device MAX aggregates as the legacy SQL.
// Retention and link provenance are checked before contributing any field.
func batchActivityRow(record EvidenceBatchRecord, device string, now, historySince, asOf time.Time) (activityRawRow, bool, error) {
	o := record.Observation
	if o == nil || !record.ObservationExpiresAt.After(now) || o.Kind != "device-neighbor-seen" {
		return activityRawRow{}, false, nil
	}
	row := activityRawRow{ID: o.ID, Source: DeviceActivitySource{ObservationID: o.ID, SensorID: o.SensorID, Kind: o.Kind, SourceStream: o.SourceStream, IngestedAt: o.IngestedAt.UTC(), Attribution: o.Attribution}}
	claims := map[string]domain.IdentityClaim{}
	for _, c := range record.Claims {
		if c.Claim.SourceObservationID == o.ID && c.ExpiresAt.After(now) && !c.Claim.ObservedAt.Before(historySince) && !c.Claim.ObservedAt.After(asOf) {
			claims[c.Claim.ID] = c.Claim
		}
	}
	found := false
	for _, l := range record.Links {
		if l.DeviceID != device || l.EvidenceObservationID != o.ID || l.ValidFrom.After(asOf) {
			continue
		}
		c, ok := claims[l.ClaimID]
		if !ok {
			continue
		}
		if !found || c.ObservedAt.UnixNano() > row.At {
			row.At = c.ObservedAt.UnixNano()
			found = true
		}
		value, err := domain.NormalizeClaimValue(c.Kind, c.Value)
		if err != nil {
			return activityRawRow{}, false, err
		}
		if c.Kind == domain.ClaimMAC && value > row.Hardware {
			row.Hardware = value
		}
		if c.Kind == domain.ClaimIPv4 || c.Kind == domain.ClaimIPv6 {
			if string(c.Kind) > row.Family {
				row.Family = string(c.Kind)
			}
			if value > row.Address {
				row.Address = value
			}
		}
	}
	return row, found && row.Hardware != "" && row.Address != "", nil
}
