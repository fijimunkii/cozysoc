package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	DeviceActivityWindow        = 24 * time.Hour
	deviceActivityHistoryWindow = 7 * 24 * time.Hour
	MaxDeviceActivityItems      = 100
)

type DeviceActivityKind string

const (
	DeviceActivityFirstObserved  DeviceActivityKind = "first-observed"
	DeviceActivityAddressChanged DeviceActivityKind = "address-changed"
	DeviceActivityObserved       DeviceActivityKind = "observed"
)

type DeviceActivitySource struct {
	ObservationID string
	SensorID      string
	Kind          string
	SourceStream  string
	IngestedAt    time.Time
	Attribution   string
}

type DeviceActivityItem struct {
	ID              string
	Kind            DeviceActivityKind
	At              time.Time
	DeviceID        string
	UserLabel       string
	AddressFamily   string
	Address         string
	PreviousAddress string
	HardwareAddress string
	Source          DeviceActivitySource
}

type DeviceActivityQuery struct {
	ScopeID string
	AsOf    time.Time
	Limit   int
}

type DeviceActivityPage struct {
	Since     time.Time
	Items     []DeviceActivityItem
	Truncated bool
}

func (s *Store) ListDeviceActivity(ctx context.Context, query DeviceActivityQuery) (DeviceActivityPage, error) {
	if err := validateQueryID("scope id", query.ScopeID); err != nil {
		return DeviceActivityPage{}, err
	}
	if query.AsOf.IsZero() {
		query.AsOf = s.now().UTC()
	} else {
		query.AsOf = query.AsOf.UTC()
	}
	if query.Limit == 0 {
		query.Limit = MaxDeviceActivityItems
	}
	if query.Limit < 1 || query.Limit > MaxDeviceActivityItems {
		return DeviceActivityPage{}, fmt.Errorf("device activity limit must be between 1 and %d", MaxDeviceActivityItems)
	}

	now := s.now().UTC()
	since := query.AsOf.Add(-DeviceActivityWindow)
	historySince := query.AsOf.Add(-deviceActivityHistoryWindow)
	rows, err := s.conn.QueryContext(ctx, `WITH raw AS (
		SELECT
			o.id AS observation_id,
			d.id AS device_id,
			d.user_label AS user_label,
			d.created_at_ns AS created_at_ns,
			MAX(c.observed_at_ns) AS observed_at_ns,
			MAX(CASE WHEN c.kind = 'mac' THEN c.value END) AS hardware_address,
			MAX(CASE WHEN c.kind IN ('ipv4', 'ipv6') THEN c.kind END) AS address_family,
			MAX(CASE WHEN c.kind IN ('ipv4', 'ipv6') THEN c.value END) AS address,
			o.sensor_id AS sensor_id,
			o.kind AS source_kind,
			o.source_stream AS source_stream,
			o.ingested_at_ns AS ingested_at_ns,
			o.attribution AS attribution
		FROM observations o
		JOIN device_claim_links l ON l.evidence_observation_id = o.id
		JOIN identity_claims c ON c.id = l.claim_id AND c.source_observation_id = o.id
		JOIN devices d ON d.id = l.device_id
		WHERE o.scope_id = ?
		  AND o.kind = 'device-neighbor-seen'
		  AND o.expires_at_ns > ?
		  AND c.expires_at_ns > ?
		  AND c.observed_at_ns >= ?
		  AND c.observed_at_ns <= ?
		  AND l.valid_from_ns <= ?
		  AND (d.retired_at_ns IS NULL OR d.retired_at_ns >= ?)
		GROUP BY o.id, d.id, d.user_label, d.created_at_ns, o.sensor_id, o.kind, o.source_stream, o.ingested_at_ns, o.attribution
		HAVING MAX(CASE WHEN c.kind = 'mac' THEN c.value END) IS NOT NULL
		   AND MAX(CASE WHEN c.kind IN ('ipv4', 'ipv6') THEN c.value END) IS NOT NULL
	), sequenced AS (
		SELECT raw.*,
			LAG(address) OVER (PARTITION BY device_id, address_family ORDER BY observed_at_ns ASC, observation_id ASC) AS previous_address,
			ROW_NUMBER() OVER (PARTITION BY device_id ORDER BY observed_at_ns ASC, observation_id ASC) AS first_rank,
			ROW_NUMBER() OVER (PARTITION BY device_id ORDER BY observed_at_ns DESC, observation_id DESC) AS latest_rank
		FROM raw
	), classified AS (
		SELECT sequenced.*,
			CASE
				WHEN first_rank = 1 AND observed_at_ns = created_at_ns THEN 'first-observed'
				WHEN previous_address IS NOT NULL AND previous_address <> address THEN 'address-changed'
				ELSE 'observed'
			END AS activity_kind
		FROM sequenced
	), selected AS (
		SELECT * FROM classified
		WHERE observed_at_ns >= ? AND (
			activity_kind IN ('first-observed', 'address-changed')
			OR (latest_rank = 1 AND observed_at_ns <> created_at_ns)
		)
	)
	SELECT observation_id, activity_kind, observed_at_ns, device_id, user_label,
		address_family, address, previous_address, hardware_address,
		sensor_id, source_kind, source_stream, ingested_at_ns, attribution
	FROM selected
	ORDER BY observed_at_ns DESC, observation_id ASC
	LIMIT ?`,
		query.ScopeID,
		unixNanos(now), unixNanos(now), unixNanos(historySince), unixNanos(query.AsOf), unixNanos(query.AsOf), unixNanos(query.AsOf),
		unixNanos(since), query.Limit+1,
	)
	if err != nil {
		return DeviceActivityPage{}, fmt.Errorf("list device activity: %w", err)
	}
	defer rows.Close()

	page := DeviceActivityPage{Since: since, Items: make([]DeviceActivityItem, 0, query.Limit+1)}
	for rows.Next() {
		var item DeviceActivityItem
		var at, ingestedAt int64
		var userLabel, previousAddress sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Kind, &at, &item.DeviceID, &userLabel,
			&item.AddressFamily, &item.Address, &previousAddress, &item.HardwareAddress,
			&item.Source.SensorID, &item.Source.Kind, &item.Source.SourceStream, &ingestedAt, &item.Source.Attribution,
		); err != nil {
			return DeviceActivityPage{}, fmt.Errorf("scan device activity: %w", err)
		}
		item.At = time.Unix(0, at).UTC()
		item.Source.ObservationID = item.ID
		item.Source.IngestedAt = time.Unix(0, ingestedAt).UTC()
		if userLabel.Valid {
			item.UserLabel = userLabel.String
		}
		if previousAddress.Valid && item.Kind == DeviceActivityAddressChanged {
			item.PreviousAddress = previousAddress.String
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return DeviceActivityPage{}, fmt.Errorf("iterate device activity: %w", err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		page.Truncated = true
	}
	return page, nil
}
