package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const MaxDeviceLabelBytes = 160

var ErrDeviceNotInScope = errors.New("device is not available in the requested scope")

func ValidateDeviceLabel(label string) error {
	if label == "" {
		return nil
	}
	if len(label) > MaxDeviceLabelBytes || strings.TrimSpace(label) != label {
		return fmt.Errorf("device label is oversized or untrimmed")
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return fmt.Errorf("device label contains control characters")
		}
	}
	return nil
}

// SetDeviceLabel updates a user label only when the device has retained evidence
// in the explicitly selected network scope. The label transition and audit event
// commit in one SQLite transaction.
func (s *Store) SetDeviceLabel(ctx context.Context, scopeID, deviceID, label string) (bool, error) {
	if s == nil || s.conn == nil {
		return false, fmt.Errorf("storage is unavailable")
	}
	if err := validateQueryID("scope id", scopeID); err != nil {
		return false, err
	}
	if err := validateQueryID("device id", deviceID); err != nil {
		return false, err
	}
	if err := ValidateDeviceLabel(label); err != nil {
		return false, err
	}

	now := s.now().UTC()
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return false, err
	}

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin device label transaction: %w", err)
	}
	defer tx.Rollback()
	merges, err := loadDeviceMerges(ctx, tx, scopeID)
	if err != nil {
		return false, err
	}
	if merges.bySource[deviceID] != "" {
		return false, ErrDeviceNotInScope
	}

	// Authorize labels against the same corrected, scope-local evidence view
	// shown to the user. A source whose only links were split away is absent;
	// a new target with retained batch links is available.
	snapshot, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		return false, fmt.Errorf("resolve device label evidence: %w", err)
	}
	detail, err := snapshot.GetDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: scopeID, DeviceID: deviceID, AsOf: now, Limit: 1})
	if errors.Is(err, ErrDeviceEvidenceNotFound) {
		return false, fmt.Errorf("%w: %s", ErrDeviceNotInScope, deviceID)
	}
	if err != nil {
		return false, fmt.Errorf("resolve device label evidence: %w", err)
	}
	previous := detail.Summary.Device.UserLabel
	if previous == label {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `UPDATE devices SET user_label = ? WHERE id = ?`, nullableString(label), deviceID); err != nil {
		return false, wrapWrite("update device label", err)
	}

	payload, err := json.Marshal(map[string]any{
		"schema_version":     2,
		"state":              "applied",
		"scope_id":           scopeID,
		"device_id":          deviceID,
		"previous_label_set": previous != "",
		"label_set":          label != "",
	})
	if err != nil {
		return false, fmt.Errorf("encode device label audit payload: %w", err)
	}
	auditID, err := randomID("audit.device-label")
	if err != nil {
		return false, err
	}
	event := domain.AuditEvent{
		ID:            auditID,
		Kind:          "device-label",
		Actor:         "local-os-user",
		OccurredAt:    now,
		SchemaVersion: 2,
		Payload:       payload,
		Retention:     domain.RetentionAudit,
	}
	if err := domain.ValidateAuditEvent(event); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events
		(id, kind, actor, occurred_at_ns, schema_version, payload, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Kind, event.Actor, unixNanos(event.OccurredAt), event.SchemaVersion,
		string(event.Payload), event.Retention, expiresAt); err != nil {
		return false, wrapWrite("insert device label audit event", err)
	}

	if err := tx.Commit(); err != nil {
		return false, wrapWrite("commit device label transaction", err)
	}
	return true, nil
}
