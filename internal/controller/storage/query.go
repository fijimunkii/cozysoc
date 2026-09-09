package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	DefaultQueryLimit = 100
	MaxQueryLimit     = 200
	MaxQueryWindow    = 31 * 24 * time.Hour
)

type ObservationCursor struct {
	IngestedAt time.Time `json:"ingested_at"`
	ID         string    `json:"id"`
}

type ObservationQuery struct {
	ScopeID  string
	SensorID string
	Kind     string
	Since    time.Time
	Until    time.Time
	Before   *ObservationCursor
	Limit    int
}

type ObservationPage struct {
	Observations []domain.Observation `json:"observations"`
	Next         *ObservationCursor   `json:"next,omitempty"`
}

type DeviceQuery struct {
	ScopeID string
	AsOf    time.Time
	AfterID string
	Limit   int
}

type DevicePage struct {
	Devices []domain.Device `json:"devices"`
	NextID  string          `json:"next_id,omitempty"`
}

func (s *Store) ListObservations(ctx context.Context, query ObservationQuery) (ObservationPage, error) {
	query, err := s.normalizeObservationQuery(query)
	if err != nil {
		return ObservationPage{}, err
	}

	clauses := []string{
		"scope_id = ?",
		"ingested_at_ns >= ?",
		"ingested_at_ns <= ?",
		"expires_at_ns > ?",
	}
	args := []any{query.ScopeID, unixNanos(query.Since), unixNanos(query.Until), unixNanos(s.now().UTC())}
	if query.SensorID != "" {
		clauses = append(clauses, "sensor_id = ?")
		args = append(args, query.SensorID)
	}
	if query.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, query.Kind)
	}
	if query.Before != nil {
		clauses = append(clauses, "(ingested_at_ns < ? OR (ingested_at_ns = ? AND id > ?))")
		cursorTime := unixNanos(query.Before.IngestedAt)
		args = append(args, cursorTime, cursorTime, query.Before.ID)
	}
	args = append(args, query.Limit+1)

	rows, err := s.conn.QueryContext(ctx, `SELECT id, scope_id, sensor_id, kind, source_stream, source_key,
		source_event_id, source_time_ns, ingested_at_ns, schema_version, confidence, attribution, payload, retention_class
		FROM observations WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY ingested_at_ns DESC, id ASC LIMIT ?`, args...)
	if err != nil {
		return ObservationPage{}, fmt.Errorf("list observations: %w", err)
	}
	defer rows.Close()

	page := ObservationPage{Observations: make([]domain.Observation, 0, query.Limit+1)}
	for rows.Next() {
		observation, err := scanObservation(rows)
		if err != nil {
			return ObservationPage{}, err
		}
		page.Observations = append(page.Observations, observation)
	}
	if err := rows.Err(); err != nil {
		return ObservationPage{}, fmt.Errorf("iterate observations: %w", err)
	}
	if len(page.Observations) > query.Limit {
		page.Observations = page.Observations[:query.Limit]
		last := page.Observations[len(page.Observations)-1]
		page.Next = &ObservationCursor{IngestedAt: last.IngestedAt, ID: last.ID}
	}
	return page, nil
}

func (s *Store) ListDevicesForScope(ctx context.Context, query DeviceQuery) (DevicePage, error) {
	query, err := s.normalizeDeviceQuery(query)
	if err != nil {
		return DevicePage{}, err
	}
	now := s.now().UTC()
	args := []any{
		query.ScopeID,
		unixNanos(query.AsOf), unixNanos(query.AsOf),
		unixNanos(query.AsOf), unixNanos(query.AsOf),
		unixNanos(now),
		unixNanos(query.AsOf),
	}
	cursorClause := ""
	if query.AfterID != "" {
		cursorClause = " AND d.id > ?"
		args = append(args, query.AfterID)
	}
	args = append(args, query.Limit+1)

	rows, err := s.conn.QueryContext(ctx, `SELECT DISTINCT d.id, d.user_label, d.created_at_ns, d.retired_at_ns
		FROM devices d
		JOIN device_claim_links l ON l.device_id = d.id
		JOIN identity_claims c ON c.id = l.claim_id
		WHERE c.scope_id = ?
		  AND c.observed_at_ns <= ?
		  AND (c.valid_until_ns IS NULL OR c.valid_until_ns >= ?)
		  AND l.valid_from_ns <= ?
		  AND (l.valid_until_ns IS NULL OR l.valid_until_ns >= ?)
		  AND c.expires_at_ns > ?
		  AND (d.retired_at_ns IS NULL OR d.retired_at_ns >= ?)`+cursorClause+`
		ORDER BY d.id ASC LIMIT ?`, args...)
	if err != nil {
		return DevicePage{}, fmt.Errorf("list devices for scope: %w", err)
	}
	defer rows.Close()

	page := DevicePage{Devices: make([]domain.Device, 0, query.Limit+1)}
	for rows.Next() {
		var device domain.Device
		var userLabel sql.NullString
		var createdAt int64
		var retiredAt sql.NullInt64
		if err := rows.Scan(&device.ID, &userLabel, &createdAt, &retiredAt); err != nil {
			return DevicePage{}, fmt.Errorf("scan device: %w", err)
		}
		if userLabel.Valid {
			device.UserLabel = userLabel.String
		}
		device.CreatedAt = time.Unix(0, createdAt).UTC()
		if retiredAt.Valid {
			value := time.Unix(0, retiredAt.Int64).UTC()
			device.RetiredAt = &value
		}
		page.Devices = append(page.Devices, device)
	}
	if err := rows.Err(); err != nil {
		return DevicePage{}, fmt.Errorf("iterate devices: %w", err)
	}
	if len(page.Devices) > query.Limit {
		page.Devices = page.Devices[:query.Limit]
		page.NextID = page.Devices[len(page.Devices)-1].ID
	}
	return page, nil
}

func (s *Store) normalizeObservationQuery(query ObservationQuery) (ObservationQuery, error) {
	if err := validateQueryID("scope id", query.ScopeID); err != nil {
		return ObservationQuery{}, err
	}
	if query.SensorID != "" {
		if err := validateQueryID("sensor id", query.SensorID); err != nil {
			return ObservationQuery{}, err
		}
	}
	if query.Kind != "" && (len(query.Kind) > 96 || strings.TrimSpace(query.Kind) != query.Kind) {
		return ObservationQuery{}, fmt.Errorf("invalid observation kind filter")
	}
	if query.Until.IsZero() {
		query.Until = s.now().UTC()
	} else {
		query.Until = query.Until.UTC()
	}
	if query.Since.IsZero() {
		query.Since = query.Until.Add(-24 * time.Hour)
	} else {
		query.Since = query.Since.UTC()
	}
	if query.Until.Before(query.Since) {
		return ObservationQuery{}, fmt.Errorf("observation query until precedes since")
	}
	if query.Until.Sub(query.Since) > MaxQueryWindow {
		return ObservationQuery{}, fmt.Errorf("observation query window exceeds %s", MaxQueryWindow)
	}
	limit, err := normalizeQueryLimit(query.Limit)
	if err != nil {
		return ObservationQuery{}, err
	}
	query.Limit = limit
	if query.Before != nil {
		if query.Before.IngestedAt.IsZero() {
			return ObservationQuery{}, fmt.Errorf("observation cursor ingested_at is required")
		}
		if err := validateQueryID("observation cursor id", query.Before.ID); err != nil {
			return ObservationQuery{}, err
		}
		copyCursor := *query.Before
		copyCursor.IngestedAt = copyCursor.IngestedAt.UTC()
		query.Before = &copyCursor
	}
	return query, nil
}

func (s *Store) normalizeDeviceQuery(query DeviceQuery) (DeviceQuery, error) {
	if err := validateQueryID("scope id", query.ScopeID); err != nil {
		return DeviceQuery{}, err
	}
	if query.AsOf.IsZero() {
		query.AsOf = s.now().UTC()
	} else {
		query.AsOf = query.AsOf.UTC()
	}
	if query.AfterID != "" {
		if err := validateQueryID("device cursor id", query.AfterID); err != nil {
			return DeviceQuery{}, err
		}
	}
	limit, err := normalizeQueryLimit(query.Limit)
	if err != nil {
		return DeviceQuery{}, err
	}
	query.Limit = limit
	return query, nil
}

func normalizeQueryLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultQueryLimit, nil
	}
	if limit < 1 || limit > MaxQueryLimit {
		return 0, fmt.Errorf("query limit must be between 1 and %d", MaxQueryLimit)
	}
	return limit, nil
}

func validateQueryID(label, value string) error {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid %s", label)
	}
	for index := 0; index < len(value); index++ {
		c := value[index]
		allowed := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-'
		if !allowed || (index == 0 && !(c >= 'a' && c <= 'z')) {
			return fmt.Errorf("invalid %s", label)
		}
	}
	return nil
}

func scanObservation(rows *sql.Rows) (domain.Observation, error) {
	var observation domain.Observation
	var sourceEventID sql.NullString
	var sourceTime sql.NullInt64
	var ingestedAt int64
	var confidence sql.NullFloat64
	var payload string
	var retention string
	if err := rows.Scan(
		&observation.ID, &observation.ScopeID, &observation.SensorID, &observation.Kind,
		&observation.SourceStream, &observation.SourceKey, &sourceEventID, &sourceTime,
		&ingestedAt, &observation.SchemaVersion, &confidence, &observation.Attribution, &payload, &retention,
	); err != nil {
		return domain.Observation{}, fmt.Errorf("scan observation: %w", err)
	}
	if sourceEventID.Valid {
		observation.SourceEventID = sourceEventID.String
	}
	if sourceTime.Valid {
		value := time.Unix(0, sourceTime.Int64).UTC()
		observation.SourceTime = &value
	}
	observation.IngestedAt = time.Unix(0, ingestedAt).UTC()
	if confidence.Valid {
		value := confidence.Float64
		observation.Confidence = &value
	}
	observation.Payload = json.RawMessage(payload)
	observation.Retention = domain.RetentionClass(retention)
	return observation, nil
}
