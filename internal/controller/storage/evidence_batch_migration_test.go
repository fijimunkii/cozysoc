package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func createSchemaV3Fixture(t *testing.T, conflict bool) string {
	t.Helper()
	dir := t.TempDir()
	dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), migrationV1+migrationV2+migrationV3); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO network_scopes VALUES ('scope.fixture','lan',1,NULL,'{}');
INSERT INTO sensors VALUES ('sensor.fixture','scope.fixture','device-watch','local',1,'{}');
INSERT INTO observations VALUES ('obs.legacy','scope.fixture','sensor.fixture','device-neighbor-seen','fixture','legacy-key',NULL,NULL,10,1,NULL,'fixture','{}','standard',1000);
INSERT INTO devices VALUES ('device.legacy',NULL,10,NULL);
INSERT INTO identity_claims VALUES ('claim.legacy','scope.fixture','mac','02:00:00:00:00:01',10,20,0.75,'sensor.fixture','obs.legacy','standard',2000);
INSERT INTO device_claim_links VALUES ('link.legacy','device.legacy','claim.legacy',10,20,0.75,'inferred','fixture','obs.legacy',10);
PRAGMA user_version=3;`); err != nil {
		t.Fatal(err)
	}
	if conflict {
		// V4 creates several objects before reaching this table. The duplicate
		// name forces a late failure and proves that all earlier DDL rolls back.
		if _, err := db.ExecContext(context.Background(), `CREATE TABLE evidence_batch_identity_routes(dummy INTEGER) STRICT`); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEvidenceBatchSchemaV4MigrationPreservesLegacyAndEnablesOwner(t *testing.T) {
	dir := createSchemaV3Fixture(t, false)
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if version, err := s.SchemaVersion(ctx); err != nil || version != schemaVersion {
		t.Fatal("schema migration", version, err)
	}
	s.now = func() time.Time { return unixTime(15) }
	for table, want := range map[string]int{
		"observations": 1, "identity_claims": 1, "device_claim_links": 1,
		"evidence_batch_sources": 0, "evidence_batches": 0,
		"evidence_batch_lookup": 0, "evidence_batch_identity_groups": 0,
		"evidence_batch_identity_routes": 0,
	} {
		var got int
		if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatal(table, got, err)
		}
	}
	for kind, want := range map[string]int{"table": 5, "index": 7, "trigger": 3} {
		var got int
		if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type=? AND name LIKE 'evidence_batch%'", kind).Scan(&got); err != nil || got != want {
			t.Fatal("batch schema inventory", kind, got, err)
		}
	}
	detail, err := s.GetDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.legacy", AsOf: unixTime(15)})
	if err != nil || len(detail.Evidence) != 1 || detail.Evidence[0].Kind != domain.ClaimMAC || detail.Evidence[0].Value != "02:00:00:00:00:01" || detail.Evidence[0].Observation == nil || detail.Evidence[0].Observation.ID != "obs.legacy" {
		t.Fatal("migration changed legacy evidence", detail, err)
	}
	owner, err := NewEvidenceBatchIngestor(s, 1, nil, func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return EvidenceBatchPlan{}, errors.New("unexpected planner")
	}, unusedLegacyRepair)
	if err != nil {
		t.Fatal("installed schema unavailable to batch owner", err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.conn.PingContext(ctx); err != nil {
		t.Fatal("owner closed parent store", err)
	}
}

func TestEvidenceBatchSchemaV4MigrationFailureRollsBackAndRetries(t *testing.T) {
	dir := createSchemaV3Fixture(t, true)
	if s, err := Open(dir, DefaultLimits()); err == nil {
		s.Close()
		t.Fatal("conflicting schema accepted")
	}
	dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var version, legacy, conflict, partial int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM observations WHERE id='obs.legacy'").Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='evidence_batch_identity_routes'").Scan(&conflict); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('evidence_batch_sources','evidence_batches','evidence_batch_lookup','evidence_batch_identity_groups')").Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if version != 3 || legacy != 1 || conflict != 1 || partial != 0 {
		t.Fatal("partial migration escaped rollback", version, legacy, conflict, partial)
	}
	if _, err := db.Exec("DROP TABLE evidence_batch_identity_routes"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal("migration retry failed", err)
	}
	defer s.Close()
	if version, err := s.SchemaVersion(context.Background()); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
}

func TestEvidenceBatchSchemaV4QuotaFailureLeavesV3Retryable(t *testing.T) {
	dir := createSchemaV3Fixture(t, false)
	dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var pages, pageSize, free int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free != 0 {
		t.Fatal("fixture unexpectedly has reusable pages", free)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxBytes = pages * pageSize
	if s, err := Open(dir, limits); err == nil {
		s.Close()
		t.Fatal("migration exceeded quota")
	} else if classifyIngestionFailure(err) != IngestionFailureSQLiteFull {
		t.Fatal("migration failed for the wrong reason", err)
	}
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var version, partial, legacy int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'evidence_batch%'").Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM observations WHERE id='obs.legacy'").Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if version != 3 || partial != 0 || legacy != 1 {
		t.Fatal("quota failure changed storage", version, partial, legacy)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal("quota failure was not retryable", err)
	}
	defer s.Close()
	if version, err := s.SchemaVersion(context.Background()); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
}

func unixTime(n int64) time.Time { return time.Unix(0, n).UTC() }
