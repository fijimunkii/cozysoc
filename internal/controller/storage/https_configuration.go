package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
)

const HTTPSConfigurationAuditKind = "https-configuration"

var ErrHTTPSConfiguration = errors.New("https configuration is invalid or unavailable")

// HTTPSConfiguration owns private local settings. Disclosure is explicit;
// generic formatting/serialization must not disclose a household request.
// The record is configuration only, never approval or a fresh route binding.
type HTTPSConfiguration struct {
	ScopeID       string
	CreatedAt     time.Time
	configuration httpsplan.Configuration
}

func (HTTPSConfiguration) String() string   { return "[https configuration]" }
func (HTTPSConfiguration) GoString() string { return "[https configuration]" }
func (HTTPSConfiguration) MarshalJSON() ([]byte, error) {
	return json.Marshal("[https configuration]")
}
func (r HTTPSConfiguration) Disclosure() httpsplan.Configuration { return r.configuration }

// CreateHTTPSConfiguration always allocates new immutable references. Caller
// supplied references are rejected. To replace settings, retire the old record
// and create a new one; neither operation enables checks or recovers consent.
// There are at most 16 active and 256 lifetime records in one state directory.
func (s *Store) CreateHTTPSConfiguration(ctx context.Context, scopeID string, config httpsplan.Configuration) (HTTPSConfiguration, error) {
	if s == nil || s.conn == nil || len(scopeID) == 0 || len(scopeID) > 128 || config.Selection.ID != "" || config.Selection.EndpointID != "" || config.Selection.RequestID != "" {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	var err error
	config.Selection.ID, err = randomID("https-selection")
	if err != nil {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	config.Selection.EndpointID, err = randomID("https-endpoint")
	if err != nil {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	config.Selection.RequestID, err = randomID("https-request")
	if err != nil {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	if httpsplan.ValidateConfiguration(config) != nil {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	config.ServerName = strings.ToLower(config.ServerName)
	now := s.now().Round(0).UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	expires, err := s.expiry(domain.RetentionAudit)
	if err != nil || expires <= now.UnixNano() {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	encoded, err := encodeHTTPSConfiguration(config)
	if err != nil || len(encoded) > 4096 {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	// Explicitly encoded private disclosure belongs only in this protected table.
	// The trigger inserts a redacted audit in this same statement. No long-lived
	// writer transaction can capture concurrent run-admission or terminal audits.
	_, err = s.conn.ExecContext(ctx, `INSERT INTO https_configurations
 (id, scope_id, endpoint_id, request_id, profile, configuration, created_at_ns, audit_expires_at_ns)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, config.Selection.ID, scopeID, config.Selection.EndpointID, config.Selection.RequestID, httpsplan.Profile, string(encoded), now.UnixNano(), expires)
	if err != nil {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	return HTTPSConfiguration{ScopeID: scopeID, CreatedAt: now, configuration: config}, nil
}

// ActiveHTTPSConfiguration resolves only a live record on an unretired LAN
// scope. Call on every preflight; a previously returned copy is not authority.
// Enrollment membership and current route/source are checked by the plan/route
// layers. Reads use the existing read-only pool, never the pinned writer.
func (s *Store) ActiveHTTPSConfiguration(ctx context.Context, id string) (HTTPSConfiguration, error) {
	if s == nil || s.gatewayHistoryDB == nil || !validHTTPSSettingReference(id, "https-selection") {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return scanHTTPSConfiguration(s.gatewayHistoryDB.QueryRowContext(ctx, httpsConfigurationSelect+` AND c.id = ?`, id))
}

const httpsConfigurationSelect = `SELECT c.id, c.scope_id, c.created_at_ns, c.configuration, c.endpoint_id, c.request_id, c.profile, n.enrolled_at_ns
 FROM https_configurations c JOIN network_scopes n ON n.id = c.scope_id
 WHERE c.retired_at_ns IS NULL AND n.retired_at_ns IS NULL AND n.kind = 'lan'`

// ListActiveHTTPSConfigurations permits reloading settings after restart or an
// uncertain write response without assuming that creation failed and retrying it.
func (s *Store) ListActiveHTTPSConfigurations(ctx context.Context, scopeID string) ([]HTTPSConfiguration, error) {
	if s == nil || s.gatewayHistoryDB == nil || len(scopeID) == 0 || len(scopeID) > 128 {
		return nil, ErrHTTPSConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := s.gatewayHistoryDB.QueryContext(ctx, httpsConfigurationSelect+` AND c.scope_id = ? ORDER BY c.created_at_ns, c.id LIMIT 17`, scopeID)
	if err != nil {
		return nil, ErrHTTPSConfiguration
	}
	defer rows.Close()
	result := make([]HTTPSConfiguration, 0)
	for rows.Next() {
		item, err := scanHTTPSConfiguration(rows)
		if err != nil || len(result) == 16 {
			return nil, ErrHTTPSConfiguration
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, ErrHTTPSConfiguration
	}
	return result, nil
}

func scanHTTPSConfiguration(row interface{ Scan(...any) error }) (HTTPSConfiguration, error) {
	var result HTTPSConfiguration
	var created, enrolled int64
	var id, encoded, endpointID, requestID, profile string
	err := row.Scan(&id, &result.ScopeID, &created, &encoded, &endpointID, &requestID, &profile, &enrolled)
	if err != nil || len(encoded) > 4096 || created < enrolled {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	var decoded httpsplan.DisclosedConfiguration
	if json.Unmarshal([]byte(encoded), &decoded) != nil {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	result.configuration = httpsplan.Configuration(decoded)
	canonical, err := encodeHTTPSConfiguration(result.configuration)
	if err != nil || string(canonical) != encoded || profile != httpsplan.Profile || httpsplan.ValidateConfiguration(result.configuration) != nil ||
		result.configuration.ServerName != strings.ToLower(result.configuration.ServerName) || len(result.ScopeID) == 0 || len(result.ScopeID) > 128 ||
		!validHTTPSSettingReference(id, "https-selection") || !validHTTPSSettingReference(endpointID, "https-endpoint") || !validHTTPSSettingReference(requestID, "https-request") ||
		result.configuration.Selection.ID != id || result.configuration.Selection.EndpointID != endpointID || result.configuration.Selection.RequestID != requestID {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	result.CreatedAt = time.Unix(0, created).UTC()
	if result.CreatedAt.IsZero() {
		return HTTPSConfiguration{}, ErrHTTPSConfiguration
	}
	return result, nil
}

// RetireHTTPSConfiguration is one-way and atomically audited. Unknown or
// already retired IDs return unavailable, without creating another audit.
func (s *Store) RetireHTTPSConfiguration(ctx context.Context, id string) error {
	if s == nil || s.conn == nil || !validHTTPSSettingReference(id, "https-selection") {
		return ErrHTTPSConfiguration
	}
	now := s.now().Round(0).UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return ErrHTTPSConfiguration
	}
	expires, err := s.expiry(domain.RetentionAudit)
	if err != nil || expires <= now.UnixNano() {
		return ErrHTTPSConfiguration
	}
	result, err := s.conn.ExecContext(ctx, `UPDATE https_configurations SET retired_at_ns = ?, audit_expires_at_ns = ?
 WHERE id = ? AND retired_at_ns IS NULL AND created_at_ns <= ?`, now.UnixNano(), expires, id, now.UnixNano())
	if err != nil {
		return ErrHTTPSConfiguration
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrHTTPSConfiguration
	}
	return nil
}

func validHTTPSSettingReference(id, prefix string) bool {
	if len(id) != len(prefix)+1+32 || !strings.HasPrefix(id, prefix+".") {
		return false
	}
	for _, c := range id[len(prefix)+1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// The private SQLite representation has no HTML context. Avoid expanding every
// query ampersand so the complete 1024-byte review target fits the storage bound.
func encodeHTTPSConfiguration(config httpsplan.Configuration) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(httpsplan.DisclosedConfiguration(config)); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
