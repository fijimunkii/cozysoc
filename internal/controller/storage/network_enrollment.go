package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

var (
	ErrActiveDeviceWatchScopeExists = errors.New("an active Device Watch network scope already exists")
	deviceWatchEnrollmentMu         sync.Mutex
)

// EnrollDeviceWatchScope creates the single active v0.1 Device Watch scope and
// its durable audit event atomically. Re-enrolling the exact same metadata is
// idempotent; enrolling a different network requires a separate explicit
// retire/replace flow rather than silently switching authorization scope.
func (s *Store) EnrollDeviceWatchScope(ctx context.Context, metadata json.RawMessage) (domain.NetworkScope, bool, error) {
	if s == nil || s.conn == nil {
		return domain.NetworkScope{}, false, fmt.Errorf("storage is unavailable")
	}
	deviceWatchEnrollmentMu.Lock()
	defer deviceWatchEnrollmentMu.Unlock()
	if err := validateDeviceWatchScopeMetadata(metadata); err != nil {
		return domain.NetworkScope{}, false, err
	}

	now := s.now().UTC()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return domain.NetworkScope{}, false, fmt.Errorf("begin network enrollment transaction: %w", err)
	}
	defer tx.Rollback()

	existing, err := listActiveDeviceWatchScopesTx(ctx, tx, 2)
	if err != nil {
		return domain.NetworkScope{}, false, err
	}
	if len(existing) > 1 {
		return domain.NetworkScope{}, false, fmt.Errorf("%w: multiple active scopes violate the v0.1 enrollment invariant", ErrActiveDeviceWatchScopeExists)
	}
	if len(existing) == 1 {
		if existing[0].Kind == "lan" && equalJSON(existing[0].Metadata, metadata) {
			return existing[0], false, nil
		}
		return domain.NetworkScope{}, false, ErrActiveDeviceWatchScopeExists
	}

	scopeID, err := randomID("scope")
	if err != nil {
		return domain.NetworkScope{}, false, err
	}
	scope := domain.NetworkScope{
		ID:         scopeID,
		Kind:       "lan",
		EnrolledAt: now,
		Metadata:   append(json.RawMessage(nil), metadata...),
	}
	if err := domain.ValidateNetworkScope(scope); err != nil {
		return domain.NetworkScope{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO network_scopes
		(id, kind, enrolled_at_ns, retired_at_ns, metadata) VALUES (?, ?, ?, NULL, ?)`,
		scope.ID, scope.Kind, unixNanos(scope.EnrolledAt), string(scope.Metadata)); err != nil {
		return domain.NetworkScope{}, false, wrapWrite("enroll Device Watch network scope", err)
	}

	auditID, err := randomID("audit.network-scope")
	if err != nil {
		return domain.NetworkScope{}, false, err
	}
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return domain.NetworkScope{}, false, err
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"state":          "applied",
		"scope_id":       scope.ID,
		"kind":           scope.Kind,
		"metadata":       json.RawMessage(scope.Metadata),
	})
	if err != nil {
		return domain.NetworkScope{}, false, fmt.Errorf("encode network enrollment audit payload: %w", err)
	}
	event := domain.AuditEvent{
		ID:            auditID,
		Kind:          "network-scope-enroll",
		Actor:         "local-os-user",
		OccurredAt:    now,
		SchemaVersion: 1,
		Payload:       payload,
		Retention:     domain.RetentionAudit,
	}
	if err := domain.ValidateAuditEvent(event); err != nil {
		return domain.NetworkScope{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events
		(id, kind, actor, occurred_at_ns, schema_version, payload, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Kind, event.Actor, unixNanos(event.OccurredAt), event.SchemaVersion,
		string(event.Payload), event.Retention, expiresAt); err != nil {
		return domain.NetworkScope{}, false, wrapWrite("insert network enrollment audit event", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.NetworkScope{}, false, wrapWrite("commit network enrollment transaction", err)
	}
	return scope, true, nil
}

func (s *Store) ListActiveDeviceWatchScopes(ctx context.Context) ([]domain.NetworkScope, error) {
	if s == nil || s.conn == nil {
		return nil, fmt.Errorf("storage is unavailable")
	}
	rows, err := s.conn.QueryContext(ctx, `SELECT id, kind, enrolled_at_ns, metadata
		FROM network_scopes
		WHERE retired_at_ns IS NULL AND json_type(metadata, '$.device_watch') = 'object'
		ORDER BY enrolled_at_ns, id`)
	if err != nil {
		return nil, fmt.Errorf("list active Device Watch scopes: %w", err)
	}
	defer rows.Close()

	var scopes []domain.NetworkScope
	for rows.Next() {
		var scope domain.NetworkScope
		var enrolledAt int64
		var metadata string
		if err := rows.Scan(&scope.ID, &scope.Kind, &enrolledAt, &metadata); err != nil {
			return nil, fmt.Errorf("scan active Device Watch scope: %w", err)
		}
		scope.EnrolledAt = time.Unix(0, enrolledAt).UTC()
		scope.Metadata = json.RawMessage(metadata)
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active Device Watch scopes: %w", err)
	}
	return scopes, nil
}

func listActiveDeviceWatchScopesTx(ctx context.Context, tx *sql.Tx, limit int) ([]domain.NetworkScope, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, enrolled_at_ns, metadata
		FROM network_scopes
		WHERE retired_at_ns IS NULL AND json_type(metadata, '$.device_watch') = 'object'
		ORDER BY enrolled_at_ns, id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("resolve active Device Watch scope: %w", err)
	}
	defer rows.Close()

	var scopes []domain.NetworkScope
	for rows.Next() {
		var scope domain.NetworkScope
		var enrolledAt int64
		var metadata string
		if err := rows.Scan(&scope.ID, &scope.Kind, &enrolledAt, &metadata); err != nil {
			return nil, fmt.Errorf("scan active Device Watch scope: %w", err)
		}
		scope.EnrolledAt = time.Unix(0, enrolledAt).UTC()
		scope.Metadata = json.RawMessage(metadata)
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active Device Watch scopes: %w", err)
	}
	return scopes, nil
}

func validateDeviceWatchScopeMetadata(metadata json.RawMessage) error {
	if len(metadata) == 0 || len(metadata) > domain.MaxJSONBytes || !json.Valid(metadata) {
		return fmt.Errorf("invalid Device Watch network scope metadata")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return fmt.Errorf("decode Device Watch network scope metadata: %w", err)
	}
	raw, ok := envelope["device_watch"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("Device Watch network scope metadata is missing device_watch")
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("Device Watch network scope metadata has invalid device_watch binding")
	}
	return nil
}

func equalJSON(left, right json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
