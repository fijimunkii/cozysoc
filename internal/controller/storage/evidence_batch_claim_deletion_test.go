package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func deleteClaimFixture(t *testing.T, db *sql.DB, scope, id string, limit int) (bool, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var ok bool
	if limit == evidenceBatchClaimDeleteMaxCandidates {
		ok, err = DeleteEvidenceBatchClaim(context.Background(), tx, scope, id)
	} else {
		ok, err = deleteEvidenceBatchClaim(context.Background(), tx, scope, id, limit)
	}
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}

func sortBatchRecordsByObservation(records []EvidenceBatchRecord) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Observation == nil {
			return records[j].Observation != nil
		}
		if records[j].Observation == nil {
			return false
		}
		return records[i].Observation.ID < records[j].Observation.ID
	})
}

func TestBatchClaimDeletionMatchesLegacyAndPreservesOtherEvidence(t *testing.T) {
	for _, expired := range []bool{false, true} {
		legacy, _ := detailBatchFixture(t)
		mixed, db := detailBatchFixture(t)
		target := detailRecord(1, 0)
		other := detailRecord(2, 20*time.Minute)
		other.Claims[0].Claim.Value = "02:00:00:00:00:02"
		if expired {
			target.Claims[0].ExpiresAt = target.Claims[0].Claim.ObservedAt
		}
		for _, record := range []EvidenceBatchRecord{target, other} {
			storeLegacyDetailRecord(t, legacy, record)
			if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		claimID := target.Claims[0].Claim.ID
		if _, err := legacy.conn.ExecContext(context.Background(), "DELETE FROM identity_claims WHERE id=?", claimID); err != nil {
			t.Fatal(err)
		}
		if ok, err := deleteClaimFixture(t, db, target.Observation.ScopeID, claimID, evidenceBatchClaimDeleteMaxCandidates); err != nil || !ok {
			t.Fatal(ok, err)
		}
		expected := []EvidenceBatchRecord{target, other}
		expected[0].Claims = append([]RetainedIdentityClaim(nil), target.Claims[1:]...)
		expected[0].Links = append([]domain.DeviceClaimLink(nil), target.Links[1:]...)
		got := batchSQLRecords(t, db)
		sortBatchRecordsByObservation(expected)
		sortBatchRecordsByObservation(got)
		if !reflect.DeepEqual(got, expected) {
			t.Fatal("claim deletion changed unrelated evidence", got, expected)
		}
		var legacyClaims, legacyLinks int
		if err := legacy.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM identity_claims").Scan(&legacyClaims); err != nil {
			t.Fatal(err)
		}
		if err := legacy.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM device_claim_links").Scan(&legacyLinks); err != nil {
			t.Fatal(err)
		}
		batchClaims, batchLinks := 0, 0
		for _, record := range got {
			batchClaims += len(record.Claims)
			batchLinks += len(record.Links)
		}
		if batchClaims != legacyClaims || batchLinks != legacyLinks {
			t.Fatal("legacy parity", batchClaims, legacyClaims, batchLinks, legacyLinks)
		}
		if ok, err := deleteClaimFixture(t, db, target.Observation.ScopeID, claimID, evidenceBatchClaimDeleteMaxCandidates); err != nil || ok {
			t.Fatal("repeat deletion", ok, err)
		}
		dsn, err := sqliteFileURI(mixed.Path())
		if err != nil {
			t.Fatal(err)
		}
		reopened, err := sql.Open("sqlite", dsn+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		durable := batchSQLRecords(t, reopened)
		reopened.Close()
		sortBatchRecordsByObservation(durable)
		if !reflect.DeepEqual(durable, got) {
			t.Fatal("claim deletion did not survive reopen")
		}
	}
}

func TestBatchClaimDeletionRemovesSameScopeDuplicatesAcrossFormats(t *testing.T) {
	s, db := detailBatchFixture(t)
	first := detailRecord(1, 0)
	second := detailRecord(2, 20*time.Minute)
	claimID := first.Claims[0].Claim.ID
	second.Claims[0].Claim.ID = claimID
	second.Claims[0].Claim.Value = "02:00:00:00:00:02"
	second.Links[0].ClaimID = claimID
	for _, record := range []EvidenceBatchRecord{first, second} {
		if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	legacy := detailRecord(3, 40*time.Minute)
	legacy.Claims[0].Claim.ID = claimID
	legacy.Links[0].ClaimID = claimID
	storeLegacyDetailRecord(t, s, legacy)
	if ok, err := deleteClaimFixture(t, db, first.Observation.ScopeID, claimID, evidenceBatchClaimDeleteMaxCandidates); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM identity_claims WHERE id=?", claimID).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy duplicate survived", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM device_claim_links WHERE claim_id=?", claimID).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy dependent link survived", count, err)
	}
	for _, record := range batchSQLRecords(t, db) {
		if record.Observation == nil {
			t.Fatal("original observation removed", record)
		}
		for _, claim := range record.Claims {
			if claim.Claim.ID == claimID {
				t.Fatal("batch duplicate survived", record)
			}
		}
		for _, link := range record.Links {
			if link.ClaimID == claimID {
				t.Fatal("batch dependent link survived", record)
			}
		}
	}
	assertStageTableCount(t, db, "observations", 1)
	assertStageTableCount(t, db, "evidence_batch_lookup", 2)
}

func TestBatchClaimDeletionIsScopedAndRemovesEmptyRetainedRecords(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		s, db := detailBatchFixture(t)
		first := detailRecord(1, 0)
		other := detailRecord(2, 0)
		seedScopeAndSensor(t, s, "scope.other", "sensor.other", other.Observation.IngestedAt)
		other.Observation.ScopeID = "scope.other"
		other.Observation.SensorID = "sensor.other"
		for j := range other.Claims {
			other.Claims[j].Claim.ScopeID = "scope.other"
			other.Claims[j].Claim.SourceSensorID = "sensor.other"
		}
		claimID := first.Claims[0].Claim.ID
		other.Claims[0].Claim.ID = claimID
		other.Links[0].ClaimID = claimID
		for _, record := range []EvidenceBatchRecord{first, other} {
			if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		if ok, err := deleteClaimFixture(t, db, first.Observation.ScopeID, claimID, evidenceBatchClaimDeleteMaxCandidates); err != nil || !ok {
			t.Fatal(ok, err)
		}
		remaining := batchSQLRecords(t, db)
		foundOther := false
		for _, record := range remaining {
			for _, claim := range record.Claims {
				if claim.Claim.ID == claimID {
					foundOther = claim.Claim.ScopeID == "scope.other"
				}
			}
		}
		if !foundOther {
			t.Fatal("other scope claim was removed", remaining)
		}
		if ok, err := deleteClaimFixture(t, db, first.Observation.ScopeID, claimID, evidenceBatchClaimDeleteMaxCandidates); err != nil || ok {
			t.Fatal("other scope reported as local deletion", ok, err)
		}
	})

	t.Run("empty-record", func(t *testing.T) {
		_, db := detailBatchFixture(t)
		record := detailRecord(1, 0)
		record.Claims = record.Claims[:1]
		record.Links = record.Links[:1]
		if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if ok, err := deleteObservationFixture(t, db, record.Observation.ScopeID, record.Observation.ID); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if ok, err := deleteClaimFixture(t, db, record.Claims[0].Claim.ScopeID, record.Claims[0].Claim.ID, evidenceBatchClaimDeleteMaxCandidates); err != nil || !ok {
			t.Fatal(ok, err)
		}
		for _, table := range []string{"evidence_batches", "evidence_batch_lookup", "evidence_batch_identity_groups", "evidence_batch_identity_routes"} {
			assertStageTableCount(t, db, table, 0)
		}
	})
}

func TestBatchClaimDeletionChargesOtherScopesAndUsesPrimaryKeyCursor(t *testing.T) {
	s, db := detailBatchFixture(t)
	other := detailRecord(1, 0)
	seedScopeAndSensor(t, s, "scope.other", "sensor.other", other.Observation.IngestedAt)
	other.Observation.ScopeID = "scope.other"
	other.Observation.SensorID = "sensor.other"
	for j := range other.Claims {
		other.Claims[j].Claim.ScopeID = "scope.other"
		other.Claims[j].Claim.SourceSensorID = "sensor.other"
	}
	target := detailRecord(2, 20*time.Minute)
	for _, record := range []EvidenceBatchRecord{other, target} {
		if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if ok, err := deleteClaimFixture(t, db, target.Observation.ScopeID, target.Claims[0].Claim.ID, 1); !errors.Is(err, ErrEvidenceBatchQueryLimit) || ok {
		t.Fatal("other-scope batch escaped work budget", ok, err)
	}
	got, err := batchSQLRead(t, db, target.Observation.ScopeID, target.Observation.ID, target.Observation.IngestedAt)
	if err != nil || !reflect.DeepEqual(got, target) {
		t.Fatal("budget failure changed target", got, err)
	}
	rows, err := db.Query("EXPLAIN QUERY PLAN "+claimDeleteNextCandidateSQL, int64(1000), int64(0))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "SEARCH b USING INTEGER PRIMARY KEY")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("batch cursor did not use the primary key")
	}
}

func TestBatchClaimDeletionCrossesOldCandidateLimit(t *testing.T) {
	s, db := detailBatchFixture(t)
	other := detailRecord(1, 0)
	seedScopeAndSensor(t, s, "scope.other", "sensor.other", other.Observation.IngestedAt)
	other.Observation.ScopeID = "scope.other"
	other.Observation.SensorID = "sensor.other"
	for j := range other.Claims {
		other.Claims[j].Claim.ScopeID = "scope.other"
		other.Claims[j].Claim.SourceSensorID = "sensor.other"
	}
	if ok, err := batchSQLAppend(t, db, other); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := deleteObservationFixture(t, db, other.Observation.ScopeID, other.Observation.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var sourceBatch int64
	if err := db.QueryRow("SELECT id FROM evidence_batches").Scan(&sourceBatch); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const clone = `INSERT INTO evidence_batches(source_id,identity_group,entries,next_expiry_ns,
first_claim_ns,last_claim_ns,last_claim_expiry_ns,first_observation_ns,last_observation_ns,
last_observation_expiry_ns,data)
SELECT source_id,identity_group,entries,next_expiry_ns,first_claim_ns,last_claim_ns,
last_claim_expiry_ns,first_observation_ns,last_observation_ns,last_observation_expiry_ns,data
FROM evidence_batches WHERE id=?`
	for i := 0; i < 1024; i++ {
		if _, err := tx.Exec(clone, sourceBatch); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	target := detailRecord(2, 20*time.Minute)
	if ok, err := batchSQLAppend(t, db, target); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := deleteClaimFixture(t, db, target.Observation.ScopeID, target.Claims[0].Claim.ID, evidenceBatchClaimDeleteMaxCandidates); err != nil || !ok {
		t.Fatal("claim after 1,024 other-scope batches was not deleted", ok, err)
	}
	got, err := batchSQLRead(t, db, target.Observation.ScopeID, target.Observation.ID, target.Observation.IngestedAt)
	if err != nil || len(got.Claims) != 1 || got.Claims[0].Claim.ID != target.Claims[1].Claim.ID {
		t.Fatal("target claim survived", got, err)
	}
}

func TestBatchClaimDeletionRollsBackBudgetCorruptionAndLateFailure(t *testing.T) {
	for _, mode := range []string{"budget", "payload", "bounds", "lookup", "source", "late-legacy"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			first := detailRecord(1, 0)
			second := detailRecord(2, 20*time.Minute)
			second.Claims[0].Claim.Value = "02:00:00:00:00:02"
			second.Observation.SourceStream = "fixture.other"
			for _, record := range []EvidenceBatchRecord{first, second} {
				if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			mutations := map[string]string{
				"payload": "UPDATE evidence_batches SET data=x'00' WHERE id=(SELECT MAX(id) FROM evidence_batches)",
				"bounds":  "UPDATE evidence_batches SET last_observation_ns=last_observation_ns+1 WHERE id=(SELECT MAX(id) FROM evidence_batches)",
				"lookup":  "UPDATE evidence_batch_lookup SET slot=99 WHERE batch_id=(SELECT MAX(id) FROM evidence_batches)",
			}
			if statement, ok := mutations[mode]; ok {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "source" {
				if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("DELETE FROM evidence_batch_sources WHERE id=(SELECT source_id FROM evidence_batches ORDER BY id DESC LIMIT 1)"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "late-legacy" {
				legacy := detailRecord(3, 40*time.Minute)
				legacy.Claims[0].Claim.ID = first.Claims[0].Claim.ID
				legacy.Links[0].ClaimID = first.Claims[0].Claim.ID
				storeLegacyDetailRecord(t, s, legacy)
				if _, err := db.Exec("CREATE TRIGGER fail_claim_delete BEFORE DELETE ON identity_claims BEGIN SELECT RAISE(ABORT,'injected'); END"); err != nil {
					t.Fatal(err)
				}
			}
			var before string
			if err := db.QueryRow("SELECT group_concat(hex(data),'|') FROM (SELECT data FROM evidence_batches ORDER BY id)").Scan(&before); err != nil {
				t.Fatal(err)
			}
			limit := evidenceBatchClaimDeleteMaxCandidates
			if mode == "budget" {
				limit = 1
			}
			if ok, err := deleteClaimFixture(t, db, first.Observation.ScopeID, first.Claims[0].Claim.ID, limit); err == nil || ok || (mode == "budget" && !errors.Is(err, ErrEvidenceBatchQueryLimit)) {
				t.Fatal(ok, err)
			}
			var after string
			if err := db.QueryRow("SELECT group_concat(hex(data),'|') FROM (SELECT data FROM evidence_batches ORDER BY id)").Scan(&after); err != nil || before != after {
				t.Fatal("failed deletion changed batch evidence", err)
			}
			if mode == "late-legacy" {
				assertStageTableCount(t, db, "identity_claims", 2)
			}
		})
	}
}

func TestBatchClaimDeletionRequiresOwnerCommitForeignKeysAndContext(t *testing.T) {
	s, db := detailBatchFixture(t)
	record := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
		t.Fatal(ok, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if ok, err := DeleteEvidenceBatchClaim(context.Background(), tx, record.Observation.ScopeID, record.Claims[0].Claim.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := batchSQLRead(t, db, record.Observation.ScopeID, record.Observation.ID, record.Observation.IngestedAt)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatal("owner rollback changed evidence", got, err)
	}
	if _, err := DeleteEvidenceBatchClaim(context.Background(), tx, record.Observation.ScopeID, record.Claims[0].Claim.ID); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if ok, err := deleteClaimFixture(t, db, record.Observation.ScopeID, record.Claims[0].Claim.ID, evidenceBatchClaimDeleteMaxCandidates); err == nil || ok {
		t.Fatal(ok, err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := DeleteEvidenceBatchClaim(ctx, tx, record.Observation.ScopeID, record.Claims[0].Claim.ID); !errors.Is(err, context.Canceled) || ok {
		t.Fatal(ok, err)
	}
	if err := s.conn.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
