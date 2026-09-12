package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
)

const ResolverConfigurationAuditKind = "resolver-configuration"

var ErrResolverConfiguration = errors.New("resolver configuration is invalid or unavailable")

// ResolverConfiguration owns private local settings. Disclosure is explicit;
// generic formatting/serialization must not disclose a household query name.
// The record is configuration only, never approval or a fresh route binding.
type ResolverConfiguration struct {
	ScopeID       string
	CreatedAt     time.Time
	configuration resolverplan.Configuration
}

func (ResolverConfiguration) String() string   { return "[resolver configuration]" }
func (ResolverConfiguration) GoString() string { return "[resolver configuration]" }
func (ResolverConfiguration) MarshalJSON() ([]byte, error) {
	return json.Marshal("[resolver configuration]")
}
func (r ResolverConfiguration) Disclosure() resolverplan.Configuration { return r.configuration }

// CreateResolverConfiguration always allocates new immutable references. Caller
// supplied references are rejected. To replace settings, retire the old record
// and create a new one; neither operation enables checks or recovers consent.
// There are at most 16 active and 256 lifetime records in one state directory.
func (s *Store) CreateResolverConfiguration(ctx context.Context, scopeID string, config resolverplan.Configuration) (ResolverConfiguration, error) {
	if s == nil || s.conn == nil || len(scopeID) == 0 || len(scopeID) > 128 || config.Selection.ID != "" || config.Selection.ResolverID != "" || config.Selection.QueryID != "" {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	var err error
	config.Selection.ID, err = randomID("selection")
	if err != nil {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	config.Selection.ResolverID, err = randomID("resolver")
	if err != nil {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	config.Selection.QueryID, err = randomID("query")
	if err != nil {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	if resolverplan.ValidateConfiguration(config) != nil {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	config.Name = strings.ToLower(config.Name)
	now := s.now().Round(0).UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	expires, err := s.expiry(domain.RetentionAudit)
	if err != nil || expires <= now.UnixNano() {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	encoded, err := json.Marshal(config)
	if err != nil || len(encoded) > 4096 {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	// The trigger inserts a redacted audit in this same statement. No long-lived
	// writer transaction can capture concurrent run-admission or terminal audits.
	_, err = s.conn.ExecContext(ctx, `INSERT INTO resolver_configurations
 (id, scope_id, resolver_id, query_id, configuration, created_at_ns, audit_expires_at_ns)
 VALUES (?, ?, ?, ?, ?, ?, ?)`, config.Selection.ID, scopeID, config.Selection.ResolverID, config.Selection.QueryID, string(encoded), now.UnixNano(), expires)
	if err != nil {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	return ResolverConfiguration{ScopeID: scopeID, CreatedAt: now, configuration: config}, nil
}

// ActiveResolverConfiguration resolves only a live record on an unretired LAN
// scope. Call on every preflight; a previously returned copy is not authority.
// Enrollment membership and current route/source are checked by the plan/route
// layers. Reads use the existing read-only pool, never the pinned writer.
func (s *Store) ActiveResolverConfiguration(ctx context.Context, id string) (ResolverConfiguration, error) {
	if s == nil || s.gatewayHistoryDB == nil || len(id) == 0 || len(id) > 128 {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return scanResolverConfiguration(s.gatewayHistoryDB.QueryRowContext(ctx, resolverConfigurationSelect+` AND c.id = ?`, id))
}

const resolverConfigurationSelect = `SELECT c.id, c.scope_id, c.created_at_ns, c.configuration, c.resolver_id, c.query_id
 FROM resolver_configurations c JOIN network_scopes n ON n.id = c.scope_id
 WHERE c.retired_at_ns IS NULL AND n.retired_at_ns IS NULL AND n.kind = 'lan'`

// ListActiveResolverConfigurations permits reloading settings after restart or an
// uncertain write response without assuming that creation failed and retrying it.
func (s *Store) ListActiveResolverConfigurations(ctx context.Context, scopeID string) ([]ResolverConfiguration, error) {
	if s == nil || s.gatewayHistoryDB == nil || len(scopeID) == 0 || len(scopeID) > 128 {
		return nil, ErrResolverConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := s.gatewayHistoryDB.QueryContext(ctx, resolverConfigurationSelect+` AND c.scope_id = ? ORDER BY c.created_at_ns, c.id LIMIT 17`, scopeID)
	if err != nil {
		return nil, ErrResolverConfiguration
	}
	defer rows.Close()
	result := make([]ResolverConfiguration, 0)
	for rows.Next() {
		item, err := scanResolverConfiguration(rows)
		if err != nil || len(result) == 16 {
			return nil, ErrResolverConfiguration
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, ErrResolverConfiguration
	}
	return result, nil
}

func scanResolverConfiguration(row interface{ Scan(...any) error }) (ResolverConfiguration, error) {
	var result ResolverConfiguration
	var created int64
	var id, encoded, resolverID, queryID string
	err := row.Scan(&id, &result.ScopeID, &created, &encoded, &resolverID, &queryID)
	if err != nil || len(encoded) > 4096 {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	if json.Unmarshal([]byte(encoded), &result.configuration) != nil || resolverplan.ValidateConfiguration(result.configuration) != nil ||
		result.configuration.Selection.ID != id || result.configuration.Selection.ResolverID != resolverID || result.configuration.Selection.QueryID != queryID {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	result.CreatedAt = time.Unix(0, created).UTC()
	if result.CreatedAt.IsZero() {
		return ResolverConfiguration{}, ErrResolverConfiguration
	}
	return result, nil
}

// RetireResolverConfiguration is one-way and atomically audited. Unknown or
// already retired IDs return unavailable, without creating another audit.
func (s *Store) RetireResolverConfiguration(ctx context.Context, id string) error {
	if s == nil || s.conn == nil || len(id) == 0 || len(id) > 128 {
		return ErrResolverConfiguration
	}
	now := s.now().Round(0).UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return ErrResolverConfiguration
	}
	expires, err := s.expiry(domain.RetentionAudit)
	if err != nil || expires <= now.UnixNano() {
		return ErrResolverConfiguration
	}
	result, err := s.conn.ExecContext(ctx, `UPDATE resolver_configurations SET retired_at_ns = ?, audit_expires_at_ns = ?
 WHERE id = ? AND retired_at_ns IS NULL AND created_at_ns <= ?`, now.UnixNano(), expires, id, now.UnixNano())
	if err != nil {
		return ErrResolverConfiguration
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrResolverConfiguration
	}
	return nil
}
