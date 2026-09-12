package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	_ "modernc.org/sqlite"
)

const Filename = "cozysoc.db"

const minimumSQLiteVersion = "3.51.3"

type Limits struct {
	MaxBytes  int64
	Retention map[domain.RetentionClass]time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxBytes: 1 << 30,
		Retention: map[domain.RetentionClass]time.Duration{
			domain.RetentionEphemeral: 24 * time.Hour,
			domain.RetentionShort:     7 * 24 * time.Hour,
			domain.RetentionStandard:  30 * 24 * time.Hour,
			domain.RetentionAudit:     180 * 24 * time.Hour,
		},
	}
}

type Store struct {
	// A separate read-only pool prevents history transactions from absorbing
	// concurrent autocommit writes on the pinned writer connection.
	gatewayHistoryDB *sql.DB
	db               *sql.DB
	conn             *sql.Conn
	path             string
	limits           Limits
	now              func() time.Time
}

func Open(stateDir string, limits Limits) (*Store, error) {
	limits, err := normalizeLimits(limits)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure storage directory: %w", err)
	}

	path := filepath.Join(stateDir, Filename)
	if err := validateDatabasePath(path); err != nil {
		return nil, err
	}

	dsn, err := sqliteFileURI(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("acquire SQLite connection: %w", err)
	}
	store := &Store{db: db, conn: conn, path: path, limits: limits, now: time.Now}
	cleanup := func(cause error) (*Store, error) {
		if store.gatewayHistoryDB != nil {
			_ = store.gatewayHistoryDB.Close()
		}
		_ = conn.Close()
		_ = db.Close()
		return nil, cause
	}

	if err := store.configure(ctx); err != nil {
		return cleanup(err)
	}
	if err := store.migrate(ctx); err != nil {
		return cleanup(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return cleanup(fmt.Errorf("secure SQLite database: %w", err))
	}
	if err := store.applyQuota(ctx); err != nil {
		return cleanup(err)
	}
	store.gatewayHistoryDB, err = openGatewayHistoryDB(path)
	if err != nil {
		return cleanup(fmt.Errorf("initialize read-only gateway history: %w", err))
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var result error
	if s.gatewayHistoryDB != nil {
		result = s.gatewayHistoryDB.Close()
	}
	if s.conn != nil {
		result = errors.Join(result, s.conn.Close())
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil && result == nil {
			result = err
		}
	}
	return result
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) SQLiteVersion(ctx context.Context) (string, error) {
	var version string
	if err := s.conn.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return "", fmt.Errorf("read SQLite version: %w", err)
	}
	return version, nil
}

func (s *Store) JournalMode(ctx context.Context) (string, error) {
	var mode string
	if err := s.conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", fmt.Errorf("read SQLite journal mode: %w", err)
	}
	return strings.ToLower(mode), nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read storage schema version: %w", err)
	}
	return version, nil
}

func (s *Store) DatabaseBytes(ctx context.Context) (int64, error) {
	var pages, pageSize int64
	if err := s.conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return 0, fmt.Errorf("read SQLite page count: %w", err)
	}
	if err := s.conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read SQLite page size: %w", err)
	}
	return pages * pageSize, nil
}

func (s *Store) CreateNetworkScope(ctx context.Context, scope domain.NetworkScope) error {
	if err := domain.ValidateNetworkScope(scope); err != nil {
		return err
	}
	_, err := s.conn.ExecContext(ctx, `INSERT INTO network_scopes
		(id, kind, enrolled_at_ns, retired_at_ns, metadata) VALUES (?, ?, ?, ?, ?)`,
		scope.ID, scope.Kind, unixNanos(scope.EnrolledAt), nullableTime(scope.RetiredAt), string(scope.Metadata))
	return wrapWrite("create network scope", err)
}

func (s *Store) CreateSensor(ctx context.Context, sensor domain.Sensor) error {
	if err := domain.ValidateSensor(sensor); err != nil {
		return err
	}
	_, err := s.conn.ExecContext(ctx, `INSERT INTO sensors
		(id, scope_id, kind, ownership, registered_at_ns, metadata) VALUES (?, ?, ?, ?, ?, ?)`,
		sensor.ID, sensor.ScopeID, sensor.Kind, sensor.Ownership, unixNanos(sensor.RegisteredAt), string(sensor.Metadata))
	return wrapWrite("create sensor", err)
}

func (s *Store) InsertObservation(ctx context.Context, observation domain.Observation) (bool, error) {
	if err := domain.ValidateObservation(observation); err != nil {
		return false, err
	}
	expiresAt, err := s.expiry(observation.Retention)
	if err != nil {
		return false, err
	}
	result, err := s.conn.ExecContext(ctx, `INSERT INTO observations
		(id, scope_id, sensor_id, kind, source_stream, source_key, source_event_id, source_time_ns,
		 ingested_at_ns, schema_version, confidence, attribution, payload, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sensor_id, source_stream, source_key) DO NOTHING`,
		observation.ID, observation.ScopeID, observation.SensorID, observation.Kind,
		observation.SourceStream, observation.SourceKey, nullableString(observation.SourceEventID), nullableTime(observation.SourceTime),
		unixNanos(observation.IngestedAt), observation.SchemaVersion, nullableFloat(observation.Confidence), observation.Attribution,
		string(observation.Payload), observation.Retention, expiresAt)
	if err != nil {
		return false, wrapWrite("insert observation", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read observation insert result: %w", err)
	}
	return rows == 1, nil
}

func (s *Store) CreateDevice(ctx context.Context, device domain.Device) error {
	if err := domain.ValidateDevice(device); err != nil {
		return err
	}
	_, err := s.conn.ExecContext(ctx, `INSERT INTO devices
		(id, user_label, created_at_ns, retired_at_ns) VALUES (?, ?, ?, ?)`,
		device.ID, nullableString(device.UserLabel), unixNanos(device.CreatedAt), nullableTime(device.RetiredAt))
	return wrapWrite("create device", err)
}

func (s *Store) InsertIdentityClaim(ctx context.Context, claim domain.IdentityClaim) error {
	if err := domain.ValidateIdentityClaim(claim); err != nil {
		return err
	}
	value, err := domain.NormalizeClaimValue(claim.Kind, claim.Value)
	if err != nil {
		return err
	}
	expiresAt, err := s.expiry(claim.Retention)
	if err != nil {
		return err
	}
	_, err = s.conn.ExecContext(ctx, `INSERT INTO identity_claims
		(id, scope_id, kind, value, observed_at_ns, valid_until_ns, confidence, source_sensor_id,
		 source_observation_id, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		claim.ID, claim.ScopeID, claim.Kind, value, unixNanos(claim.ObservedAt), nullableTime(claim.ValidUntil),
		nullableFloat(claim.Confidence), claim.SourceSensorID, nullableString(claim.SourceObservationID), claim.Retention, expiresAt)
	return wrapWrite("insert identity claim", err)
}

func (s *Store) CreateDeviceClaimLink(ctx context.Context, link domain.DeviceClaimLink) error {
	if err := domain.ValidateDeviceClaimLink(link); err != nil {
		return err
	}
	_, err := s.conn.ExecContext(ctx, `INSERT INTO device_claim_links
		(id, device_id, claim_id, valid_from_ns, valid_until_ns, confidence, authority, reason,
		 evidence_observation_id, created_at_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		link.ID, link.DeviceID, link.ClaimID, unixNanos(link.ValidFrom), nullableTime(link.ValidUntil),
		nullableFloat(link.Confidence), link.Authority, link.Reason, nullableString(link.EvidenceObservationID), unixNanos(link.CreatedAt))
	return wrapWrite("create device claim link", err)
}

func (s *Store) InsertCoverageSample(ctx context.Context, sample domain.CoverageSample) error {
	if err := domain.ValidateCoverageSample(sample); err != nil {
		return err
	}
	expiresAt, err := s.expiry(sample.Retention)
	if err != nil {
		return err
	}
	_, err = s.conn.ExecContext(ctx, `INSERT INTO coverage_samples
		(id, scope_id, sensor_id, capability_id, status, started_at_ns, ended_at_ns, schema_version,
		 evidence, retention_class, expires_at_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sample.ID, sample.ScopeID, sample.SensorID, sample.CapabilityID, sample.Status,
		unixNanos(sample.StartedAt), unixNanos(sample.EndedAt), sample.SchemaVersion, string(sample.Evidence), sample.Retention, expiresAt)
	return wrapWrite("insert coverage sample", err)
}

func (s *Store) InsertFinding(ctx context.Context, finding domain.Finding) error {
	if err := domain.ValidateFinding(finding); err != nil {
		return err
	}
	expiresAt, err := s.expiry(finding.Retention)
	if err != nil {
		return err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finding transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO findings
		(id, scope_id, detector_id, detector_version, category, severity, confidence, observed_at_ns,
		 created_at_ns, schema_version, payload, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		finding.ID, finding.ScopeID, finding.DetectorID, finding.DetectorVersion, finding.Category, finding.Severity,
		nullableFloat(finding.Confidence), unixNanos(finding.ObservedAt), unixNanos(finding.CreatedAt), finding.SchemaVersion,
		string(finding.Payload), finding.Retention, expiresAt); err != nil {
		return wrapWrite("insert finding", err)
	}
	for _, observationID := range finding.EvidenceObservationIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO finding_evidence (finding_id, observation_id) VALUES (?, ?)`, finding.ID, observationID); err != nil {
			return wrapWrite("insert finding evidence", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return wrapWrite("commit finding", err)
	}
	return nil
}

func (s *Store) InsertAuditEvent(ctx context.Context, event domain.AuditEvent) error {
	if err := domain.ValidateAuditEvent(event); err != nil {
		return err
	}
	expiresAt, err := s.expiry(event.Retention)
	if err != nil {
		return err
	}
	_, err = s.conn.ExecContext(ctx, `INSERT INTO audit_events
		(id, kind, actor, occurred_at_ns, schema_version, payload, retention_class, expires_at_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Kind, event.Actor, unixNanos(event.OccurredAt), event.SchemaVersion, string(event.Payload), event.Retention, expiresAt)
	return wrapWrite("insert audit event", err)
}

func (s *Store) SaveCheckpoint(ctx context.Context, checkpoint domain.IngestionCheckpoint) error {
	if err := domain.ValidateCheckpoint(checkpoint); err != nil {
		return err
	}
	_, err := s.conn.ExecContext(ctx, `INSERT INTO ingestion_checkpoints
		(sensor_id, stream_id, cursor, updated_at_ns) VALUES (?, ?, ?, ?)
		ON CONFLICT(sensor_id, stream_id) DO UPDATE SET cursor = excluded.cursor, updated_at_ns = excluded.updated_at_ns`,
		checkpoint.SensorID, checkpoint.StreamID, checkpoint.Cursor, unixNanos(checkpoint.UpdatedAt))
	return wrapWrite("save ingestion checkpoint", err)
}

func (s *Store) LoadCheckpoint(ctx context.Context, sensorID, streamID string) (domain.IngestionCheckpoint, bool, error) {
	var checkpoint domain.IngestionCheckpoint
	var updatedAt int64
	err := s.conn.QueryRowContext(ctx, `SELECT cursor, updated_at_ns FROM ingestion_checkpoints WHERE sensor_id = ? AND stream_id = ?`, sensorID, streamID).
		Scan(&checkpoint.Cursor, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IngestionCheckpoint{}, false, nil
	}
	if err != nil {
		return domain.IngestionCheckpoint{}, false, fmt.Errorf("load ingestion checkpoint: %w", err)
	}
	checkpoint.SensorID = sensorID
	checkpoint.StreamID = streamID
	checkpoint.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return checkpoint, true, nil
}

func (s *Store) ObservationCount(ctx context.Context) (int, error) {
	var count int
	if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM observations").Scan(&count); err != nil {
		return 0, fmt.Errorf("count observations: %w", err)
	}
	return count, nil
}

func (s *Store) IdentityClaimCount(ctx context.Context, scopeID string, kind domain.ClaimKind, value string) (int, error) {
	normalized, err := domain.NormalizeClaimValue(kind, value)
	if err != nil {
		return 0, err
	}
	var count int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM identity_claims WHERE scope_id = ? AND kind = ? AND value = ?`, scopeID, kind, normalized).Scan(&count); err != nil {
		return 0, fmt.Errorf("count identity claims: %w", err)
	}
	return count, nil
}

func (s *Store) PruneExpired(ctx context.Context, now time.Time, maxRows int) (map[string]int64, error) {
	if maxRows <= 0 || maxRows > 10_000 {
		return nil, fmt.Errorf("prune maxRows must be between 1 and 10000")
	}
	tables := []string{"identity_claims", "findings", "coverage_samples", "observations", "audit_events", "storage_events"}
	counts := make(map[string]int64, len(tables))
	total := int64(0)
	for _, table := range tables {
		query := fmt.Sprintf("DELETE FROM %s WHERE id IN (SELECT id FROM %s WHERE expires_at_ns <= ? ORDER BY expires_at_ns, id LIMIT ?)", table, table)
		result, err := s.conn.ExecContext(ctx, query, unixNanos(now), maxRows)
		if err != nil {
			return nil, wrapWrite("prune "+table, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read prune result for %s: %w", table, err)
		}
		if rows > 0 {
			counts[table] = rows
			total += rows
		}
	}
	if total > 0 {
		details, _ := json.Marshal(map[string]any{"expired_rows": counts, "total": total})
		if err := s.insertStorageEvent(ctx, "retention-expired", now, details); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func (s *Store) StorageEventCount(ctx context.Context, kind string) (int, error) {
	var count int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM storage_events WHERE kind = ?`, kind).Scan(&count); err != nil {
		return 0, fmt.Errorf("count storage events: %w", err)
	}
	return count, nil
}

func (s *Store) configure(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = FULL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA trusted_schema = OFF",
	} {
		if _, err := s.conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite (%s): %w", statement, err)
		}
	}
	var journal string
	if err := s.conn.QueryRowContext(ctx, "PRAGMA journal_mode = DELETE").Scan(&journal); err != nil {
		return fmt.Errorf("configure SQLite rollback journal: %w", err)
	}
	if strings.ToLower(journal) != "delete" {
		return fmt.Errorf("SQLite journal mode = %q, want delete", journal)
	}
	version, err := s.SQLiteVersion(ctx)
	if err != nil {
		return err
	}
	if compareVersion(version, minimumSQLiteVersion) < 0 {
		return fmt.Errorf("SQLite %s is too old; require %s or newer", version, minimumSQLiteVersion)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("storage schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	if version != 0 {
		return fmt.Errorf("no migration path from storage schema version %d", version)
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storage migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, migrationV1); err != nil {
		return fmt.Errorf("apply storage schema v1: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set storage schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage migration: %w", err)
	}
	return nil
}

func (s *Store) applyQuota(ctx context.Context) error {
	var pageSize, pageCount int64
	if err := s.conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return fmt.Errorf("read SQLite page size: %w", err)
	}
	if err := s.conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return fmt.Errorf("read SQLite page count: %w", err)
	}
	maxPages := s.limits.MaxBytes / pageSize
	if maxPages < pageCount {
		return fmt.Errorf("existing database uses %d pages but configured quota permits %d", pageCount, maxPages)
	}
	if maxPages < 1 {
		return fmt.Errorf("configured database quota is smaller than one SQLite page")
	}
	var effective int64
	pragma := "PRAGMA max_page_count = " + strconv.FormatInt(maxPages, 10)
	if err := s.conn.QueryRowContext(ctx, pragma).Scan(&effective); err != nil {
		return fmt.Errorf("configure SQLite max_page_count: %w", err)
	}
	if effective > maxPages {
		return fmt.Errorf("SQLite max_page_count %d exceeds requested limit %d", effective, maxPages)
	}
	return nil
}

func (s *Store) expiry(class domain.RetentionClass) (int64, error) {
	duration, ok := s.limits.Retention[class]
	if !ok || duration <= 0 {
		return 0, fmt.Errorf("retention class %q has no positive duration", class)
	}
	return unixNanos(s.now().UTC().Add(duration)), nil
}

func (s *Store) insertStorageEvent(ctx context.Context, kind string, occurredAt time.Time, details json.RawMessage) error {
	id, err := randomID("storage")
	if err != nil {
		return err
	}
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return err
	}
	_, err = s.conn.ExecContext(ctx, `INSERT INTO storage_events (id, kind, occurred_at_ns, details, expires_at_ns) VALUES (?, ?, ?, ?, ?)`,
		id, kind, unixNanos(occurredAt), string(details), expiresAt)
	return wrapWrite("insert storage event", err)
}

func normalizeLimits(limits Limits) (Limits, error) {
	if limits.MaxBytes == 0 && limits.Retention == nil {
		limits = DefaultLimits()
	}
	if limits.MaxBytes <= 0 {
		return Limits{}, fmt.Errorf("storage MaxBytes must be positive")
	}
	if limits.Retention == nil {
		limits.Retention = DefaultLimits().Retention
	}
	copyRetention := make(map[domain.RetentionClass]time.Duration, len(limits.Retention))
	for _, class := range []domain.RetentionClass{domain.RetentionEphemeral, domain.RetentionShort, domain.RetentionStandard, domain.RetentionAudit} {
		duration, ok := limits.Retention[class]
		if !ok || duration <= 0 {
			return Limits{}, fmt.Errorf("retention class %q must have a positive duration", class)
		}
		copyRetention[class] = duration
	}
	limits.Retention = copyRetention
	return limits, nil
}

func validateDatabasePath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect SQLite database path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular SQLite database path %s", path)
	}
	return nil
}

func wrapWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func unixNanos(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return unixNanos(*value)
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func randomID(prefix string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate storage event id: %w", err)
	}
	return prefix + "." + hex.EncodeToString(buffer), nil
}

func compareVersion(actual, minimum string) int {
	a := parseVersion(actual)
	b := parseVersion(minimum)
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(value string) [3]int {
	var result [3]int
	parts := strings.Split(value, ".")
	for i := 0; i < len(parts) && i < len(result); i++ {
		result[i], _ = strconv.Atoi(parts[i])
	}
	return result
}
