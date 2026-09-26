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

const MaxDeviceMergesPerScope = 64

var (
	ErrDeviceMergeConflict = errors.New("device identity merge conflicts with an existing correction")
	ErrDeviceMergeLimit    = errors.New("device identity correction limit reached")
)

// The active projection is a set of stars: a source maps directly to one
// unmerged target. Keeping it acyclic makes one read resolve both historical
// evidence and new reconciliation without chasing mutable chains.
type deviceMergeMap struct {
	bySource        map[string]string
	sourcesByTarget map[string][]string
}

type DeviceMerge struct {
	SourceDeviceID string    `json:"source_device_id"`
	TargetDeviceID string    `json:"target_device_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListDeviceMerges exposes the bounded current projection for an explicit
// scope. The audit log preserves each transition, including undone merges.
func (s *Store) ListDeviceMerges(ctx context.Context, scopeID string) ([]DeviceMerge, error) {
	if s == nil || s.gatewayHistoryDB == nil {
		return nil, fmt.Errorf("storage is unavailable")
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return nil, err
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := loadDeviceMerges(ctx, tx, scopeID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT source_device_id,target_device_id,created_at_ns
		FROM device_identity_merges WHERE scope_id=? ORDER BY source_device_id LIMIT ?`, scopeID, MaxDeviceMergesPerScope+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DeviceMerge, 0)
	for rows.Next() {
		var item DeviceMerge
		var created int64
		if err := rows.Scan(&item.SourceDeviceID, &item.TargetDeviceID, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = time.Unix(0, created).UTC()
		if !batchTimeFits(item.CreatedAt) {
			return nil, ErrEvidenceBatchData
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m deviceMergeMap) canonical(id string) string {
	if target := m.bySource[id]; target != "" {
		return target
	}
	return id
}

func loadDeviceMerges(ctx context.Context, tx *sql.Tx, scopeID string) (deviceMergeMap, error) {
	result := deviceMergeMap{bySource: map[string]string{}, sourcesByTarget: map[string][]string{}}
	if tx == nil {
		return result, ErrEvidenceBatchData
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT source_device_id,target_device_id FROM device_identity_merges
		WHERE scope_id=? ORDER BY source_device_id LIMIT ?`, scopeID, MaxDeviceMergesPerScope+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			return result, err
		}
		if len(result.bySource) >= MaxDeviceMergesPerScope {
			return result, ErrDeviceMergeLimit
		}
		if validateQueryID("source device id", source) != nil || validateQueryID("target device id", target) != nil || source == target {
			return result, ErrEvidenceBatchData
		}
		result.bySource[source] = target
		result.sourcesByTarget[target] = append(result.sourcesByTarget[target], source)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	for target := range result.sourcesByTarget {
		if result.bySource[target] != "" {
			return result, ErrEvidenceBatchData
		}
	}
	return result, nil
}

// MergeDevices changes only the current identity projection. Both original
// devices must have retained evidence in the same scope; claims, links and
// observations remain untouched. A repeated identical merge is a no-op.
func (s *Store) MergeDevices(ctx context.Context, scopeID, sourceID, targetID string) (bool, error) {
	if s == nil || s.conn == nil {
		return false, fmt.Errorf("storage is unavailable")
	}
	for label, id := range map[string]string{"scope id": scopeID, "source device id": sourceID, "target device id": targetID} {
		if err := validateQueryID(label, id); err != nil {
			return false, err
		}
	}
	if sourceID == targetID {
		return false, ErrDeviceMergeConflict
	}
	now := s.now().UTC()
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return false, err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin device merge: %w", err)
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM network_scopes WHERE id=? AND kind='lan' AND retired_at_ns IS NULL`, scopeID).Scan(&active); err != nil {
		return false, err
	}
	if active != 1 {
		return false, ErrDeviceNotInScope
	}
	merges, err := loadDeviceMerges(ctx, tx, scopeID)
	if err != nil {
		return false, err
	}
	if merges.bySource[sourceID] == targetID {
		return false, nil
	}
	if merges.bySource[sourceID] != "" || merges.bySource[targetID] != "" || len(merges.sourcesByTarget[sourceID]) != 0 {
		return false, ErrDeviceMergeConflict
	}
	if len(merges.bySource) >= MaxDeviceMergesPerScope {
		return false, ErrDeviceMergeLimit
	}
	splits, err := loadDeviceSplits(ctx, tx, scopeID)
	if err != nil {
		return false, err
	}
	for _, id := range []string{sourceID, targetID} {
		if len(splits.bySource[id]) != 0 || len(splits.byTarget[id]) != 0 {
			return false, ErrDeviceMergeConflict
		}
	}
	view, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		return false, err
	}
	for _, id := range []string{sourceID, targetID} {
		if _, err := view.getOriginalDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: scopeID, DeviceID: id, AsOf: now, Limit: 1}); err != nil {
			if errors.Is(err, ErrDeviceEvidenceNotFound) {
				return false, ErrDeviceNotInScope
			}
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_identity_merges(scope_id,source_device_id,target_device_id,created_at_ns)
		VALUES(?,?,?,?)`, scopeID, sourceID, targetID, now.UnixNano()); err != nil {
		return false, wrapWrite("insert device identity merge", err)
	}
	if err := appendDeviceIdentityAudit(ctx, tx, "merge", scopeID, sourceID, targetID, now, expiresAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, wrapWrite("commit device identity merge", err)
	}
	return true, nil
}

// UnmergeDevices restores the source's original evidence projection. It does
// not rewrite historical links or claim that a different split was performed.
func (s *Store) UnmergeDevices(ctx context.Context, scopeID, sourceID string) (bool, error) {
	if s == nil || s.conn == nil {
		return false, fmt.Errorf("storage is unavailable")
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return false, err
	}
	if err := validateQueryID("source device id", sourceID); err != nil {
		return false, err
	}
	now := s.now().UTC()
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return false, err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin device unmerge: %w", err)
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM network_scopes WHERE id=? AND kind='lan' AND retired_at_ns IS NULL`, scopeID).Scan(&active); err != nil {
		return false, err
	}
	if active != 1 {
		return false, ErrDeviceNotInScope
	}
	var targetID string
	err = tx.QueryRowContext(ctx, `SELECT target_device_id FROM device_identity_merges WHERE scope_id=? AND source_device_id=?`, scopeID, sourceID).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_identity_merges WHERE scope_id=? AND source_device_id=?`, scopeID, sourceID); err != nil {
		return false, wrapWrite("remove device identity merge", err)
	}
	if err := appendDeviceIdentityAudit(ctx, tx, "unmerge", scopeID, sourceID, targetID, now, expiresAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, wrapWrite("commit device identity unmerge", err)
	}
	return true, nil
}

func appendDeviceIdentityAudit(ctx context.Context, tx *sql.Tx, action, scopeID, sourceID, targetID string, now time.Time, expiresAt int64, observationID ...string) error {
	fields := map[string]any{
		"schema_version":   1,
		"action":           action,
		"scope_id":         scopeID,
		"source_device_id": sourceID,
		"target_device_id": targetID,
	}
	if len(observationID) > 0 {
		fields["observation_id"] = observationID[0]
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	id, err := randomID("audit.device-identity")
	if err != nil {
		return err
	}
	event := domain.AuditEvent{ID: id, Kind: "device-identity", Actor: "local-os-user", OccurredAt: now, SchemaVersion: 1, Payload: payload, Retention: domain.RetentionAudit}
	if err := domain.ValidateAuditEvent(event); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,kind,actor,occurred_at_ns,schema_version,payload,retention_class,expires_at_ns)
		VALUES(?,?,?,?,?,?,?,?)`, id, event.Kind, event.Actor, now.UnixNano(), 1, string(payload), event.Retention, expiresAt); err != nil {
		return wrapWrite("insert device identity audit", err)
	}
	return nil
}
