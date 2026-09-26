package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	MaxDeviceSplitsPerScope    = 64
	splitProjectionBatchBudget = 512
)

var (
	ErrDeviceSplitConflict = errors.New("device observation split conflicts with an existing correction")
	ErrDeviceSplitLimit    = errors.New("device observation split limit reached")
)

type DeviceSplit struct {
	ObservationID  string    `json:"observation_id"`
	SourceDeviceID string    `json:"source_device_id"`
	TargetDeviceID string    `json:"target_device_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type deviceSplitMap struct {
	byLink             map[string]DeviceSplit
	byObservation      map[string]DeviceSplit
	bySource           map[string][]DeviceSplit
	byTarget           map[string][]DeviceSplit
	byValue            map[string][]deviceSplitClaimRoute
	linksByObservation map[string][]deviceSplitLinkRoute
}

type deviceSplitLinkRoute struct {
	LinkID     string
	ObservedAt time.Time
}

type deviceSplitClaimRoute struct {
	DeviceSplit
	ObservedAt time.Time
}

func splitClaimKey(kind domain.ClaimKind, value string) string { return string(kind) + "\x00" + value }

func sortDeviceSplits(items []DeviceSplit) {
	sort.Slice(items, func(i, j int) bool { return items[i].ObservationID < items[j].ObservationID })
}

func loadDeviceSplits(ctx context.Context, tx *sql.Tx, scopeID string) (deviceSplitMap, error) {
	result := deviceSplitMap{byLink: map[string]DeviceSplit{}, byObservation: map[string]DeviceSplit{}, bySource: map[string][]DeviceSplit{}, byTarget: map[string][]DeviceSplit{}, byValue: map[string][]deviceSplitClaimRoute{}, linksByObservation: map[string][]deviceSplitLinkRoute{}}
	if tx == nil {
		return result, ErrEvidenceBatchData
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.observation_id,s.source_device_id,s.target_device_id,s.created_at_ns,l.link_id,l.kind,l.value,l.observed_at_ns
		FROM device_identity_splits s JOIN device_identity_split_links l USING(scope_id,observation_id)
		WHERE s.scope_id=? ORDER BY s.observation_id,l.link_id LIMIT ?`, scopeID, MaxDeviceSplitsPerScope*4+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DeviceSplit
		var linkID, value string
		var kind domain.ClaimKind
		var created, observed int64
		if err := rows.Scan(&item.ObservationID, &item.SourceDeviceID, &item.TargetDeviceID, &created, &linkID, &kind, &value, &observed); err != nil {
			return result, err
		}
		item.CreatedAt = time.Unix(0, created).UTC()
		observedAt := time.Unix(0, observed).UTC()
		if len(result.byLink) >= MaxDeviceSplitsPerScope*4 {
			return result, ErrEvidenceBatchQueryLimit
		}
		if validateQueryID("observation id", item.ObservationID) != nil || validateQueryID("source device id", item.SourceDeviceID) != nil || validateQueryID("target device id", item.TargetDeviceID) != nil || validateQueryID("link id", linkID) != nil || item.SourceDeviceID == item.TargetDeviceID || !batchTimeFits(item.CreatedAt) || !batchTimeFits(observedAt) {
			return result, ErrEvidenceBatchData
		}
		if normalized, err := domain.NormalizeClaimValue(kind, value); err != nil || normalized != value {
			return result, ErrEvidenceBatchData
		}
		if previous, ok := result.byObservation[item.ObservationID]; ok {
			if previous != item {
				return result, ErrEvidenceBatchData
			}
		} else {
			if len(result.byObservation) >= MaxDeviceSplitsPerScope {
				return result, ErrEvidenceBatchQueryLimit
			}
			result.byObservation[item.ObservationID] = item
			result.bySource[item.SourceDeviceID] = append(result.bySource[item.SourceDeviceID], item)
			result.byTarget[item.TargetDeviceID] = append(result.byTarget[item.TargetDeviceID], item)
		}
		result.byLink[linkID] = item
		result.linksByObservation[item.ObservationID] = append(result.linksByObservation[item.ObservationID], deviceSplitLinkRoute{LinkID: linkID, ObservedAt: observedAt})
		key := splitClaimKey(kind, value)
		result.byValue[key] = append(result.byValue[key], deviceSplitClaimRoute{DeviceSplit: item, ObservedAt: observedAt})
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) ListDeviceSplits(ctx context.Context, scopeID string) ([]DeviceSplit, error) {
	if s == nil || s.gatewayHistoryDB == nil {
		return nil, fmt.Errorf("storage is unavailable")
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	splits, err := loadDeviceSplits(ctx, tx, scopeID)
	if err != nil {
		return nil, err
	}
	result := make([]DeviceSplit, 0, len(splits.byObservation))
	for _, item := range splits.byObservation {
		result = append(result, item)
	}
	sortDeviceSplits(result)
	return result, nil
}

// SplitDeviceObservation moves the reviewed observation's retained links into
// a separate current Device projection. Original claims and links remain
// untouched, and a repeated request returns the same target.
func (s *Store) SplitDeviceObservation(ctx context.Context, scopeID, sourceID, observationID, targetID string) (string, bool, error) {
	if s == nil || s.conn == nil {
		return "", false, fmt.Errorf("storage is unavailable")
	}
	for label, id := range map[string]string{"scope id": scopeID, "source device id": sourceID, "observation id": observationID} {
		if err := validateQueryID(label, id); err != nil {
			return "", false, err
		}
	}
	if targetID != "" {
		if err := validateQueryID("target device id", targetID); err != nil {
			return "", false, err
		}
		if targetID == sourceID {
			return "", false, ErrDeviceSplitConflict
		}
	}
	now := s.now().UTC()
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return "", false, err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin device split: %w", err)
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM network_scopes WHERE id=? AND kind='lan' AND retired_at_ns IS NULL`, scopeID).Scan(&active); err != nil {
		return "", false, err
	}
	if active != 1 {
		return "", false, ErrDeviceNotInScope
	}
	splits, err := loadDeviceSplits(ctx, tx, scopeID)
	if err != nil {
		return "", false, err
	}
	if existing, ok := splits.byObservation[observationID]; ok {
		if existing.SourceDeviceID != sourceID || targetID != "" && existing.TargetDeviceID != targetID {
			return "", false, ErrDeviceSplitConflict
		}
		return existing.TargetDeviceID, false, nil
	}
	if len(splits.byObservation) >= MaxDeviceSplitsPerScope {
		return "", false, ErrDeviceSplitLimit
	}
	merges, err := loadDeviceMerges(ctx, tx, scopeID)
	if err != nil {
		return "", false, err
	}
	if merges.bySource[sourceID] != "" || len(merges.sourcesByTarget[sourceID]) != 0 || targetID != "" && (merges.bySource[targetID] != "" || len(merges.sourcesByTarget[targetID]) != 0) {
		return "", false, ErrDeviceSplitConflict
	}
	view, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		return "", false, err
	}
	rows, err := view.getOriginalDeviceEvidenceRowsBudget(ctx, DeviceEvidenceDetailQuery{ScopeID: scopeID, DeviceID: sourceID, AsOf: now, Limit: MaxDeviceDetailEvidence}, nil)
	if errors.Is(err, ErrDeviceEvidenceNotFound) {
		return "", false, ErrDeviceNotInScope
	}
	if err != nil {
		return "", false, err
	}
	selected := make([]deviceEvidenceRow, 0, 2)
	for _, row := range rows.Evidence {
		if row.Evidence.Observation != nil && row.Evidence.Observation.ID == observationID {
			if row.Evidence.Observation.Kind != "device-neighbor-seen" || row.Evidence.Authority != domain.LinkInferred {
				return "", false, ErrDeviceSplitConflict
			}
			selected = append(selected, row)
		}
	}
	if len(selected) == 0 {
		return "", false, ErrDeviceNotInScope
	}
	if len(selected) > 4 || len(rows.Evidence) > MaxDeviceDetailEvidence && rows.Evidence[MaxDeviceDetailEvidence].Evidence.ObservedAt.Equal(selected[0].Evidence.ObservedAt) {
		return "", false, ErrEvidenceBatchQueryLimit
	}
	for _, row := range selected[1:] {
		if !row.Evidence.ObservedAt.Equal(selected[0].Evidence.ObservedAt) {
			return "", false, ErrDeviceSplitConflict
		}
	}
	if targetID == "" {
		targetID, err = randomID("device.split")
		if err != nil {
			return "", false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id,created_at_ns) VALUES(?,?)`, targetID, selected[0].Evidence.ObservedAt.UnixNano()); err != nil {
			return "", false, wrapWrite("create split device", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_identity_split_targets(device_id,scope_id,created_at_ns) VALUES(?,?,?)`, targetID, scopeID, now.UnixNano()); err != nil {
			return "", false, wrapWrite("register split device", err)
		}
	} else {
		if _, err := view.getOriginalDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: scopeID, DeviceID: targetID, AsOf: now, Limit: 1}); errors.Is(err, ErrDeviceEvidenceNotFound) {
			if len(splits.byTarget[targetID]) == 0 {
				return "", false, ErrDeviceNotInScope
			}
		} else if err != nil {
			return "", false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_identity_splits(scope_id,observation_id,source_device_id,target_device_id,created_at_ns) VALUES(?,?,?,?,?)`, scopeID, observationID, sourceID, targetID, now.UnixNano()); err != nil {
		return "", false, wrapWrite("insert device split", err)
	}
	for _, row := range selected {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_identity_split_links(scope_id,observation_id,link_id,kind,value,observed_at_ns) VALUES(?,?,?,?,?,?)`, scopeID, observationID, row.LinkID, row.Evidence.Kind, row.Evidence.Value, row.Evidence.ObservedAt.UnixNano()); err != nil {
			return "", false, wrapWrite("insert device split link", err)
		}
	}
	if err := appendDeviceIdentityAudit(ctx, tx, "split", scopeID, sourceID, targetID, now, expiresAt, observationID); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, wrapWrite("commit device split", err)
	}
	return targetID, true, nil
}

func (s *Store) UndoDeviceSplitObservation(ctx context.Context, scopeID, observationID string) (bool, error) {
	if s == nil || s.conn == nil {
		return false, fmt.Errorf("storage is unavailable")
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return false, err
	}
	if err := validateQueryID("observation id", observationID); err != nil {
		return false, err
	}
	now := s.now().UTC()
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return false, err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sourceID, targetID string
	err = tx.QueryRowContext(ctx, `SELECT source_device_id,target_device_id FROM device_identity_splits WHERE scope_id=? AND observation_id=?`, scopeID, observationID).Scan(&sourceID, &targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_identity_splits WHERE scope_id=? AND observation_id=?`, scopeID, observationID); err != nil {
		return false, wrapWrite("remove device split", err)
	}
	var createdTarget bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_identity_split_targets WHERE scope_id=? AND device_id=?)`, scopeID, targetID).Scan(&createdTarget); err != nil {
		return false, err
	}
	if createdTarget {
		var stillUsed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_identity_splits WHERE scope_id=? AND target_device_id=?)`, scopeID, targetID).Scan(&stillUsed); err != nil {
			return false, err
		}
		if !stillUsed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM device_identity_split_targets WHERE scope_id=? AND device_id=?`, scopeID, targetID); err != nil {
				return false, wrapWrite("remove split device registration", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id=?
				AND NOT EXISTS(SELECT 1 FROM device_claim_links WHERE device_id=?)
				AND NOT EXISTS(SELECT 1 FROM evidence_batch_identity_routes WHERE device_id=?)
				AND NOT EXISTS(SELECT 1 FROM device_identity_splits WHERE source_device_id=? OR target_device_id=?)`, targetID, targetID, targetID, targetID, targetID); err != nil {
				return false, wrapWrite("remove empty split device", err)
			}
		}
	}
	if err := appendDeviceIdentityAudit(ctx, tx, "unsplit", scopeID, sourceID, targetID, now, expiresAt, observationID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, wrapWrite("commit device unsplit", err)
	}
	return true, nil
}
