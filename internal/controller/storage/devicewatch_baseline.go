package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HasContinuousDeviceWatchCoverage checks the latest earlier collection for
// this exact observation point. A first collection, unavailable sources, or collection
// gap cannot establish that a newly seen identity arrived after a baseline.
// It reads in the same transaction as identity planning and evidence insertion.
func (s *MixedIdentitySnapshot) HasContinuousDeviceWatchCoverage(ctx context.Context, scopeID, sensorID string, before time.Time, maxGap time.Duration) (bool, error) {
	if s == nil || s.legacy == nil || s.legacy.tx == nil || before.IsZero() || maxGap <= 0 {
		return false, fmt.Errorf("Device Watch coverage snapshot is unavailable")
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return false, err
	}
	if err := validateQueryID("sensor id", sensorID); err != nil {
		return false, err
	}
	var status string
	var ended int64
	err := s.legacy.tx.QueryRowContext(ctx, `SELECT status,ended_at_ns FROM coverage_samples
		WHERE scope_id=? AND sensor_id=? AND capability_id='device-watch'
		AND ended_at_ns<? AND expires_at_ns>?
		ORDER BY ended_at_ns DESC,id DESC LIMIT 1`, scopeID, sensorID, before.UnixNano(), s.legacy.now.UnixNano()).Scan(&status, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read prior Device Watch coverage: %w", err)
	}
	return status == "partial" && before.Sub(time.Unix(0, ended).UTC()) <= maxGap, nil
}
