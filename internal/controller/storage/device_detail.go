package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const MaxDeviceDetailEvidence = 100

var ErrDeviceEvidenceNotFound = errors.New("device evidence is not available in scope")

type DeviceEvidenceObservation struct {
	ID           string
	SensorID     string
	Kind         string
	SourceStream string
	IngestedAt   time.Time
	Attribution  string
}

type DeviceIdentityEvidence struct {
	Kind            domain.ClaimKind
	Value           string
	ObservedAt      time.Time
	ClaimValidUntil *time.Time
	ClaimConfidence *float64
	SourceSensorID  string
	LinkValidUntil  *time.Time
	LinkConfidence  *float64
	Authority       domain.LinkAuthority
	Reason          string
	Observation     *DeviceEvidenceObservation
}

type DeviceEvidenceDetailQuery struct {
	ScopeID  string
	DeviceID string
	AsOf     time.Time
	Limit    int
}

type DeviceEvidenceDetail struct {
	Summary   DeviceEvidenceSummary
	Evidence  []DeviceIdentityEvidence
	Truncated bool
}

func (s *Store) GetDeviceEvidenceDetail(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceEvidenceDetail, error) {
	if err := validateQueryID("scope id", query.ScopeID); err != nil {
		return DeviceEvidenceDetail{}, err
	}
	if err := validateQueryID("device id", query.DeviceID); err != nil {
		return DeviceEvidenceDetail{}, err
	}
	if query.AsOf.IsZero() {
		query.AsOf = s.now().UTC()
	} else {
		query.AsOf = query.AsOf.UTC()
	}
	if query.Limit == 0 {
		query.Limit = MaxDeviceDetailEvidence
	}
	if query.Limit < 1 || query.Limit > MaxDeviceDetailEvidence {
		return DeviceEvidenceDetail{}, fmt.Errorf("device detail evidence limit must be between 1 and %d", MaxDeviceDetailEvidence)
	}
	now := s.now().UTC()

	var summary DeviceEvidenceSummary
	var userLabel sql.NullString
	var createdAt, lastSeen int64
	var retiredAt sql.NullInt64
	err := s.conn.QueryRowContext(ctx, `SELECT d.id, d.user_label, d.created_at_ns, d.retired_at_ns,
		MAX(c.observed_at_ns) AS last_seen_ns
		FROM devices d
		JOIN device_claim_links l ON l.device_id = d.id
		JOIN identity_claims c ON c.id = l.claim_id
		WHERE d.id = ? AND c.scope_id = ?
		  AND c.observed_at_ns <= ?
		  AND c.expires_at_ns > ?
		  AND l.valid_from_ns <= ?
		  AND (d.retired_at_ns IS NULL OR d.retired_at_ns >= ?)
		GROUP BY d.id, d.user_label, d.created_at_ns, d.retired_at_ns`,
		query.DeviceID, query.ScopeID, unixNanos(query.AsOf), unixNanos(now), unixNanos(query.AsOf), unixNanos(query.AsOf)).
		Scan(&summary.Device.ID, &userLabel, &createdAt, &retiredAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceEvidenceDetail{}, fmt.Errorf("%w: %s", ErrDeviceEvidenceNotFound, query.DeviceID)
	}
	if err != nil {
		return DeviceEvidenceDetail{}, fmt.Errorf("get device evidence summary: %w", err)
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

	rows, err := s.conn.QueryContext(ctx, `SELECT c.kind, c.value, c.observed_at_ns, c.valid_until_ns, c.confidence,
		c.source_sensor_id, l.valid_until_ns, l.confidence, l.authority, l.reason,
		o.id, o.sensor_id, o.kind, o.source_stream, o.ingested_at_ns, o.attribution
		FROM device_claim_links l
		JOIN identity_claims c ON c.id = l.claim_id
		LEFT JOIN observations o ON o.id = c.source_observation_id AND o.expires_at_ns > ?
		WHERE l.device_id = ? AND c.scope_id = ?
		  AND c.observed_at_ns <= ?
		  AND c.expires_at_ns > ?
		  AND l.valid_from_ns <= ?
		ORDER BY c.observed_at_ns DESC, c.id ASC LIMIT ?`,
		unixNanos(now), query.DeviceID, query.ScopeID, unixNanos(query.AsOf), unixNanos(now), unixNanos(query.AsOf), query.Limit+1)
	if err != nil {
		return DeviceEvidenceDetail{}, fmt.Errorf("list device identity evidence: %w", err)
	}
	defer rows.Close()

	detail := DeviceEvidenceDetail{Summary: summary, Evidence: make([]DeviceIdentityEvidence, 0, query.Limit+1)}
	for rows.Next() {
		var item DeviceIdentityEvidence
		var observedAt int64
		var claimValidUntil, linkValidUntil sql.NullInt64
		var claimConfidence, linkConfidence sql.NullFloat64
		var authority string
		var observationID, observationSensorID, observationKind, sourceStream, attribution sql.NullString
		var observationIngestedAt sql.NullInt64
		if err := rows.Scan(
			&item.Kind, &item.Value, &observedAt, &claimValidUntil, &claimConfidence,
			&item.SourceSensorID, &linkValidUntil, &linkConfidence, &authority, &item.Reason,
			&observationID, &observationSensorID, &observationKind, &sourceStream, &observationIngestedAt, &attribution,
		); err != nil {
			return DeviceEvidenceDetail{}, fmt.Errorf("scan device identity evidence: %w", err)
		}
		item.ObservedAt = time.Unix(0, observedAt).UTC()
		if claimValidUntil.Valid {
			value := time.Unix(0, claimValidUntil.Int64).UTC()
			item.ClaimValidUntil = &value
		}
		if linkValidUntil.Valid {
			value := time.Unix(0, linkValidUntil.Int64).UTC()
			item.LinkValidUntil = &value
		}
		if claimConfidence.Valid {
			value := claimConfidence.Float64
			item.ClaimConfidence = &value
		}
		if linkConfidence.Valid {
			value := linkConfidence.Float64
			item.LinkConfidence = &value
		}
		item.Authority = domain.LinkAuthority(authority)
		if observationID.Valid {
			if !observationSensorID.Valid || !observationKind.Valid || !sourceStream.Valid || !observationIngestedAt.Valid || !attribution.Valid {
				return DeviceEvidenceDetail{}, fmt.Errorf("device identity evidence has incomplete observation provenance")
			}
			if observationSensorID.String != item.SourceSensorID {
				return DeviceEvidenceDetail{}, fmt.Errorf("device identity evidence source sensor mismatch")
			}
			item.Observation = &DeviceEvidenceObservation{
				ID:           observationID.String,
				SensorID:     observationSensorID.String,
				Kind:         observationKind.String,
				SourceStream: sourceStream.String,
				IngestedAt:   time.Unix(0, observationIngestedAt.Int64).UTC(),
				Attribution:  attribution.String,
			}
		}
		detail.Evidence = append(detail.Evidence, item)
	}
	if err := rows.Err(); err != nil {
		return DeviceEvidenceDetail{}, fmt.Errorf("iterate device identity evidence: %w", err)
	}
	if len(detail.Evidence) > query.Limit {
		detail.Evidence = detail.Evidence[:query.Limit]
		detail.Truncated = true
	}
	return detail, nil
}
