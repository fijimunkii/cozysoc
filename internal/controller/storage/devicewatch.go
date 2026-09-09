package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

var ErrNetworkScopeNotFound = errors.New("network scope not found")

type DeviceEvidenceSummary struct {
	Device    domain.Device `json:"device"`
	FirstSeen time.Time     `json:"first_seen"`
	LastSeen  time.Time     `json:"last_seen"`
}

type DeviceEvidenceQuery struct {
	ScopeID string
	AsOf    time.Time
	AfterID string
	Limit   int
}

type DeviceEvidencePage struct {
	Devices []DeviceEvidenceSummary `json:"devices"`
	NextID  string                  `json:"next_id,omitempty"`
}

func (s *Store) GetNetworkScope(ctx context.Context, id string) (domain.NetworkScope, error) {
	if err := validateQueryID("network scope id", id); err != nil {
		return domain.NetworkScope{}, err
	}
	var scope domain.NetworkScope
	var enrolledAt int64
	var retiredAt sql.NullInt64
	var metadata string
	err := s.conn.QueryRowContext(ctx, `SELECT id, kind, enrolled_at_ns, retired_at_ns, metadata FROM network_scopes WHERE id = ?`, id).
		Scan(&scope.ID, &scope.Kind, &enrolledAt, &retiredAt, &metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NetworkScope{}, fmt.Errorf("%w: %s", ErrNetworkScopeNotFound, id)
	}
	if err != nil {
		return domain.NetworkScope{}, fmt.Errorf("get network scope: %w", err)
	}
	scope.EnrolledAt = time.Unix(0, enrolledAt).UTC()
	if retiredAt.Valid {
		value := time.Unix(0, retiredAt.Int64).UTC()
		scope.RetiredAt = &value
	}
	scope.Metadata = json.RawMessage(metadata)
	return scope, nil
}

func (s *Store) EnsureSensor(ctx context.Context, sensor domain.Sensor) error {
	if err := domain.ValidateSensor(sensor); err != nil {
		return err
	}
	if _, err := s.conn.ExecContext(ctx, `INSERT INTO sensors
		(id, scope_id, kind, ownership, registered_at_ns, metadata) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, sensor.ID, sensor.ScopeID, sensor.Kind, sensor.Ownership,
		unixNanos(sensor.RegisteredAt), string(sensor.Metadata)); err != nil {
		return wrapWrite("ensure sensor", err)
	}

	var scopeID, kind, ownership, metadata string
	if err := s.conn.QueryRowContext(ctx, `SELECT scope_id, kind, ownership, metadata FROM sensors WHERE id = ?`, sensor.ID).
		Scan(&scopeID, &kind, &ownership, &metadata); err != nil {
		return fmt.Errorf("verify ensured sensor: %w", err)
	}
	if scopeID != sensor.ScopeID || kind != sensor.Kind || ownership != sensor.Ownership || metadata != string(sensor.Metadata) {
		return fmt.Errorf("sensor %q already exists with different immutable identity", sensor.ID)
	}
	return nil
}

func (s *Store) EnsureDevice(ctx context.Context, device domain.Device) error {
	if err := domain.ValidateDevice(device); err != nil {
		return err
	}
	if _, err := s.conn.ExecContext(ctx, `INSERT INTO devices
		(id, user_label, created_at_ns, retired_at_ns) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, device.ID, nullableString(device.UserLabel), unixNanos(device.CreatedAt), nullableTime(device.RetiredAt)); err != nil {
		return wrapWrite("ensure device", err)
	}
	var createdAt int64
	if err := s.conn.QueryRowContext(ctx, `SELECT created_at_ns FROM devices WHERE id = ?`, device.ID).Scan(&createdAt); err != nil {
		return fmt.Errorf("verify ensured device: %w", err)
	}
	if createdAt != unixNanos(device.CreatedAt) {
		return fmt.Errorf("device %q already exists with different creation evidence", device.ID)
	}
	return nil
}

func (s *Store) EnsureIdentityClaim(ctx context.Context, claim domain.IdentityClaim) (string, error) {
	if err := domain.ValidateIdentityClaim(claim); err != nil {
		return "", err
	}
	if claim.SourceObservationID == "" {
		return "", fmt.Errorf("idempotent identity claim requires source observation id")
	}
	value, err := domain.NormalizeClaimValue(claim.Kind, claim.Value)
	if err != nil {
		return "", err
	}
	expiresAt, err := s.expiry(claim.Retention)
	if err != nil {
		return "", err
	}
	if _, err := s.conn.ExecContext(ctx, `INSERT INTO identity_claims
		(id, scope_id, kind, value, observed_at_ns, valid_until_ns, confidence, source_sensor_id,
		 source_observation_id, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, claim.ID, claim.ScopeID, claim.Kind, value, unixNanos(claim.ObservedAt),
		nullableTime(claim.ValidUntil), nullableFloat(claim.Confidence), claim.SourceSensorID,
		claim.SourceObservationID, claim.Retention, expiresAt); err != nil {
		return "", wrapWrite("ensure identity claim", err)
	}

	var id, scopeID, sensorID string
	err = s.conn.QueryRowContext(ctx, `SELECT id, scope_id, source_sensor_id FROM identity_claims
		WHERE source_observation_id = ? AND kind = ? AND value = ?`, claim.SourceObservationID, claim.Kind, value).
		Scan(&id, &scopeID, &sensorID)
	if err != nil {
		return "", fmt.Errorf("verify ensured identity claim: %w", err)
	}
	if scopeID != claim.ScopeID || sensorID != claim.SourceSensorID {
		return "", fmt.Errorf("identity claim source key already belongs to different provenance")
	}
	return id, nil
}

func (s *Store) EnsureDeviceClaimLink(ctx context.Context, link domain.DeviceClaimLink) error {
	if err := domain.ValidateDeviceClaimLink(link); err != nil {
		return err
	}
	if _, err := s.conn.ExecContext(ctx, `INSERT INTO device_claim_links
		(id, device_id, claim_id, valid_from_ns, valid_until_ns, confidence, authority, reason,
		 evidence_observation_id, created_at_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, link.ID, link.DeviceID, link.ClaimID, unixNanos(link.ValidFrom), nullableTime(link.ValidUntil),
		nullableFloat(link.Confidence), link.Authority, link.Reason, nullableString(link.EvidenceObservationID), unixNanos(link.CreatedAt)); err != nil {
		return wrapWrite("ensure device claim link", err)
	}
	var deviceID, claimID string
	if err := s.conn.QueryRowContext(ctx, `SELECT device_id, claim_id FROM device_claim_links WHERE id = ?`, link.ID).
		Scan(&deviceID, &claimID); err != nil {
		return fmt.Errorf("verify ensured device claim link: %w", err)
	}
	if deviceID != link.DeviceID || claimID != link.ClaimID {
		return fmt.Errorf("device claim link %q already exists with different identity", link.ID)
	}
	return nil
}

func (s *Store) FindRecentDevicesByClaim(ctx context.Context, scopeID string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
	if err := validateQueryID("scope id", scopeID); err != nil {
		return nil, err
	}
	if since.IsZero() || until.IsZero() || until.Before(since) || until.Sub(since) > MaxQueryWindow {
		return nil, fmt.Errorf("invalid recent-device claim window")
	}
	normalized, err := domain.NormalizeClaimValue(kind, value)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.QueryContext(ctx, `SELECT DISTINCT d.id, d.user_label, d.created_at_ns, d.retired_at_ns
		FROM devices d
		JOIN device_claim_links l ON l.device_id = d.id
		JOIN identity_claims c ON c.id = l.claim_id
		WHERE c.scope_id = ? AND c.kind = ? AND c.value = ?
		  AND c.observed_at_ns >= ? AND c.observed_at_ns <= ?
		  AND c.expires_at_ns > ?
		  AND (d.retired_at_ns IS NULL OR d.retired_at_ns >= ?)
		ORDER BY d.id ASC LIMIT 3`, scopeID, kind, normalized, unixNanos(since.UTC()), unixNanos(until.UTC()),
		unixNanos(s.now().UTC()), unixNanos(until.UTC()))
	if err != nil {
		return nil, fmt.Errorf("find recent devices by claim: %w", err)
	}
	defer rows.Close()

	devices := make([]domain.Device, 0, 3)
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent devices by claim: %w", err)
	}
	return devices, nil
}

func (s *Store) ListDeviceEvidence(ctx context.Context, query DeviceEvidenceQuery) (DeviceEvidencePage, error) {
	if err := validateQueryID("scope id", query.ScopeID); err != nil {
		return DeviceEvidencePage{}, err
	}
	if query.AsOf.IsZero() {
		query.AsOf = s.now().UTC()
	} else {
		query.AsOf = query.AsOf.UTC()
	}
	if query.AfterID != "" {
		if err := validateQueryID("device cursor id", query.AfterID); err != nil {
			return DeviceEvidencePage{}, err
		}
	}
	limit, err := normalizeQueryLimit(query.Limit)
	if err != nil {
		return DeviceEvidencePage{}, err
	}
	query.Limit = limit

	args := []any{query.ScopeID, unixNanos(query.AsOf), unixNanos(s.now().UTC()), unixNanos(query.AsOf)}
	cursor := ""
	if query.AfterID != "" {
		cursor = " AND d.id > ?"
		args = append(args, query.AfterID)
	}
	args = append(args, query.Limit+1)
	rows, err := s.conn.QueryContext(ctx, `SELECT d.id, d.user_label, d.created_at_ns, d.retired_at_ns,
		MAX(c.observed_at_ns) AS last_seen_ns
		FROM devices d
		JOIN device_claim_links l ON l.device_id = d.id
		JOIN identity_claims c ON c.id = l.claim_id
		WHERE c.scope_id = ?
		  AND c.observed_at_ns <= ?
		  AND c.expires_at_ns > ?
		  AND (d.retired_at_ns IS NULL OR d.retired_at_ns >= ?)`+cursor+`
		GROUP BY d.id, d.user_label, d.created_at_ns, d.retired_at_ns
		ORDER BY d.id ASC LIMIT ?`, args...)
	if err != nil {
		return DeviceEvidencePage{}, fmt.Errorf("list device evidence: %w", err)
	}
	defer rows.Close()

	page := DeviceEvidencePage{Devices: make([]DeviceEvidenceSummary, 0, query.Limit+1)}
	for rows.Next() {
		var summary DeviceEvidenceSummary
		var userLabel sql.NullString
		var createdAt, lastSeen int64
		var retiredAt sql.NullInt64
		if err := rows.Scan(&summary.Device.ID, &userLabel, &createdAt, &retiredAt, &lastSeen); err != nil {
			return DeviceEvidencePage{}, fmt.Errorf("scan device evidence: %w", err)
		}
		if userLabel.Valid {
			summary.Device.UserLabel = userLabel.String
		}
		summary.Device.CreatedAt = time.Unix(0, createdAt).UTC()
		if retiredAt.Valid {
			value := time.Unix(0, retiredAt.Int64).UTC()
			summary.Device.RetiredAt = &value
		}
		summary.FirstSeen = summary.Device.CreatedAt
		summary.LastSeen = time.Unix(0, lastSeen).UTC()
		page.Devices = append(page.Devices, summary)
	}
	if err := rows.Err(); err != nil {
		return DeviceEvidencePage{}, fmt.Errorf("iterate device evidence: %w", err)
	}
	if len(page.Devices) > query.Limit {
		page.Devices = page.Devices[:query.Limit]
		page.NextID = page.Devices[len(page.Devices)-1].Device.ID
	}
	return page, nil
}

func scanDevice(rows *sql.Rows) (domain.Device, error) {
	var device domain.Device
	var userLabel sql.NullString
	var createdAt int64
	var retiredAt sql.NullInt64
	if err := rows.Scan(&device.ID, &userLabel, &createdAt, &retiredAt); err != nil {
		return domain.Device{}, fmt.Errorf("scan device: %w", err)
	}
	if userLabel.Valid {
		device.UserLabel = userLabel.String
	}
	device.CreatedAt = time.Unix(0, createdAt).UTC()
	if retiredAt.Valid {
		value := time.Unix(0, retiredAt.Int64).UTC()
		device.RetiredAt = &value
	}
	return device, nil
}
