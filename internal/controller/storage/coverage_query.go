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

// LatestCoverageSample returns the newest retained coverage evidence for one
// capability inside one authorized network scope. Ordering uses the sample's
// evidence time, not insertion order, so replaying historical data cannot make
// an old sample appear current.
func (s *Store) LatestCoverageSample(ctx context.Context, scopeID, capabilityID string) (domain.CoverageSample, bool, error) {
	if s == nil || s.conn == nil {
		return domain.CoverageSample{}, false, fmt.Errorf("storage is unavailable")
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return domain.CoverageSample{}, false, err
	}
	if err := validateQueryID("capability id", capabilityID); err != nil {
		return domain.CoverageSample{}, false, err
	}

	var sample domain.CoverageSample
	var startedAt, endedAt int64
	var evidence string
	var retention string
	err := s.conn.QueryRowContext(ctx, `SELECT id, scope_id, sensor_id, capability_id, status,
		started_at_ns, ended_at_ns, schema_version, evidence, retention_class
		FROM coverage_samples
		WHERE scope_id = ? AND capability_id = ? AND expires_at_ns > ?
		ORDER BY ended_at_ns DESC, id ASC
		LIMIT 1`, scopeID, capabilityID, unixNanos(s.now().UTC())).Scan(
		&sample.ID,
		&sample.ScopeID,
		&sample.SensorID,
		&sample.CapabilityID,
		&sample.Status,
		&startedAt,
		&endedAt,
		&sample.SchemaVersion,
		&evidence,
		&retention,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CoverageSample{}, false, nil
	}
	if err != nil {
		return domain.CoverageSample{}, false, fmt.Errorf("load latest coverage sample: %w", err)
	}

	sample.StartedAt = time.Unix(0, startedAt).UTC()
	sample.EndedAt = time.Unix(0, endedAt).UTC()
	sample.Evidence = json.RawMessage(evidence)
	sample.Retention = domain.RetentionClass(retention)
	return sample, true, nil
}
