package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMixedRetentionCommitsExactAuditAndIndependentExpiry(t *testing.T) {
	s, db := detailBatchFixture(t)
	batch, legacy := derivedBatchSQLRecord(), detailRecord(2, 0)
	storeLegacyDetailRecord(t, s, legacy)
	if ok, err := batchSQLAppend(t, db, batch); err != nil || !ok {
		t.Fatal(ok, err)
	}
	at := *batch.ObservationExpiresAt
	if !at.Equal(*legacy.ObservationExpiresAt) {
		t.Fatal("fixture expiry mismatch")
	}
	result, err := s.PruneEvidenceBatchExpired(context.Background(), at, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := &EvidenceBatchRetentionResult{ExpiredRows: map[string]int64{"observations": 2}, Total: 2, BatchesProcessed: 1, BatchObservations: 1}
	if !reflect.DeepEqual(result, want) {
		t.Fatal(result, want)
	}
	records := batchSQLRecords(t, db)
	if len(records) != 1 || records[0].Observation != nil || len(records[0].Claims) != 2 || len(records[0].Links) != 2 {
		t.Fatal(records)
	}
	for j, c := range records[0].Claims {
		expected := batch.Claims[j]
		expected.Claim.SourceObservationID = ""
		if !reflect.DeepEqual(c, expected) {
			t.Fatal(c, expected)
		}
	}
	for j, l := range records[0].Links {
		expected := batch.Links[j]
		expected.EvidenceObservationID = ""
		if !reflect.DeepEqual(l, expected) {
			t.Fatal(l, expected)
		}
	}
	var details []byte
	var occurred, expires int64
	if err := db.QueryRow("SELECT details,occurred_at_ns,expires_at_ns FROM storage_events WHERE kind='retention-expired'").Scan(&details, &occurred, &expires); err != nil {
		t.Fatal(err)
	}
	var audit EvidenceBatchRetentionResult
	if err := json.Unmarshal(details, &audit); err != nil || !reflect.DeepEqual(&audit, want) || occurred != at.UnixNano() || expires <= time.Now().UnixNano() {
		t.Fatal(audit, occurred, expires, err)
	}
	// An independent read-only connection observes the committed audit and evidence.
	uri, err := sqliteFileURI(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", uri+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(batchSQLRecords(t, reopened), records) {
		t.Fatal("reopened evidence differs")
	}
	assertStageTableCount(t, reopened, "storage_events", 1)
	result, err = s.PruneEvidenceBatchExpired(context.Background(), at, 100, 1)
	if err != nil || result.Total != 0 || result.BatchesProcessed != 0 {
		t.Fatal(result, err)
	}
	assertStageTableCount(t, db, "storage_events", 1)
	// Original claim deadlines, not the observation deadline, remove claims/links.
	result, err = s.PruneEvidenceBatchExpired(context.Background(), batch.Claims[1].ExpiresAt, 100, 1)
	if err != nil || result.Total != 4 || result.ExpiredRows["identity_claims"] != 4 || result.BatchClaims != 2 || result.BatchLinks != 2 {
		t.Fatal(result, err)
	}
	for _, table := range []string{"identity_claims", "device_claim_links", "evidence_batches", "evidence_batch_lookup", "evidence_batch_identity_groups", "evidence_batch_identity_routes"} {
		assertStageTableCount(t, db, table, 0)
	}
	assertStageTableCount(t, db, "storage_events", 2)
}

func TestMixedRetentionRollsBackLegacyBatchAndAuditFailures(t *testing.T) {
	for _, mode := range []string{"audit", "batch", "quota"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r, legacy := detailRecord(1, 0), detailRecord(2, 0)
			storeLegacyDetailRecord(t, s, legacy)
			if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
			statement := "CREATE TRIGGER fail_retention BEFORE INSERT ON storage_events BEGIN SELECT RAISE(ABORT,'injected audit failure'); END"
			if mode == "batch" {
				statement = "CREATE TRIGGER fail_retention BEFORE UPDATE ON evidence_batches BEGIN SELECT RAISE(ABORT,'injected batch failure'); END"
			}
			if mode == "quota" {
				if _, err := db.Exec("CREATE TABLE retention_quota_probe(id INTEGER PRIMARY KEY,data BLOB)"); err != nil {
					t.Fatal(err)
				}
				statement = "CREATE TRIGGER fail_retention BEFORE INSERT ON storage_events BEGIN INSERT INTO retention_quota_probe(data) VALUES(zeroblob(1048576)); END"
			}
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if mode == "quota" {
				size, err := s.DatabaseBytes(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				s.limits.MaxBytes = size
			}
			before := batchSQLRecords(t, db)
			result, err := s.PruneEvidenceBatchExpired(context.Background(), *r.ObservationExpiresAt, 100, 1)
			if err == nil || result != nil {
				t.Fatal("failed retention acknowledged", result, err)
			}
			if mode == "quota" && classifyIngestionFailure(err) != IngestionFailureSQLiteFull {
				t.Fatal("did not exercise SQLite quota", err)
			}
			if !reflect.DeepEqual(batchSQLRecords(t, db), before) {
				t.Fatal("batch deletion escaped rollback")
			}
			assertStageTableCount(t, db, "observations", 1)
			assertStageTableCount(t, db, "identity_claims", 2)
			assertStageTableCount(t, db, "storage_events", 0)
			if _, err := db.Exec("DROP TRIGGER fail_retention"); err != nil {
				t.Fatal(err)
			}
			result, err = s.PruneEvidenceBatchExpired(context.Background(), *r.ObservationExpiresAt, 100, 1)
			if err != nil || result.Total != 2 {
				t.Fatal("recovery failed", result, err)
			}
			assertStageTableCount(t, db, "storage_events", 1)
		})
	}
}

func TestMixedRetentionCommitFailureDoesNotAcknowledge(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	writer, err := openEvidenceBatchWriter(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.conn.ExecContext(context.Background(), "PRAGMA busy_timeout=20"); err != nil {
		t.Fatal(err)
	}
	reader, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var count int
	if err := reader.QueryRow("SELECT count(*) FROM evidence_batches").Scan(&count); err != nil {
		t.Fatal(err)
	}
	result, err := writer.pruneEvidenceBatchExpired(context.Background(), *r.ObservationExpiresAt, 100, 1)
	if err == nil || result != nil || !strings.HasPrefix(err.Error(), "commit mixed retention:") {
		t.Fatal("blocked commit acknowledged or wrong failure", result, err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt); err != nil || !reflect.DeepEqual(got, r) {
		t.Fatal(got, err)
	}
	assertStageTableCount(t, db, "storage_events", 0)
	result, err = writer.pruneEvidenceBatchExpired(context.Background(), *r.ObservationExpiresAt, 100, 1)
	if err != nil || result.Total != 1 {
		t.Fatal(result, err)
	}
	if err := s.conn.PingContext(context.Background()); err != nil {
		t.Fatal("parent was closed", err)
	}
}

func TestMixedRetentionBoundsSchemaAndCancellation(t *testing.T) {
	s, db := detailBatchFixture(t)
	at := *detailRecord(1, 0).ObservationExpiresAt
	for n := 1; n <= 3; n++ {
		r := detailRecord(n, 0)
		// Distinct source streams force three batches independently of capacity.
		r.Observation.SourceStream = fmt.Sprintf("fixture.%d", n)
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	// Each call visits one batch and at most one row from each legacy table.
	for n := 4; n <= 6; n++ {
		storeLegacyDetailRecord(t, s, detailRecord(n, 0))
	}
	result, err := s.PruneEvidenceBatchExpired(context.Background(), at, 1, 1)
	if err != nil || result.BatchesProcessed != 1 || result.BatchObservations != 1 || result.ExpiredRows["observations"] != 2 {
		t.Fatal(result, err)
	}
	assertStageTableCount(t, db, "observations", 2)
	for _, limits := range [][2]int{{0, 1}, {10001, 1}, {1, 0}, {1, 101}} {
		if result, err := s.PruneEvidenceBatchExpired(context.Background(), at, limits[0], limits[1]); err == nil || result != nil {
			t.Fatal(result, err)
		}
	}
	if result, err := s.PruneEvidenceBatchExpired(context.Background(), time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), 1, 1); err == nil || result != nil {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := s.PruneEvidenceBatchExpired(ctx, at, 1, 1); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatal(result, err)
	}
	missing := openTestStore(t)
	if _, err := missing.conn.ExecContext(context.Background(), "DROP TABLE evidence_batch_identity_routes"); err != nil {
		t.Fatal(err)
	}
	if result, err := missing.PruneEvidenceBatchExpired(context.Background(), at, 1, 1); err == nil || result != nil {
		t.Fatal("incomplete schema accepted", result, err)
	}
	if err := missing.conn.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := (*Store)(nil).PruneEvidenceBatchExpired(context.Background(), at, 1, 1); err == nil || result != nil {
		t.Fatal(result, err)
	}
}
