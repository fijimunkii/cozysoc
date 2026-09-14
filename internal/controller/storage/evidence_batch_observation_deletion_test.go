package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func deleteObservationFixture(t *testing.T, db *sql.DB, scope, id string) (bool, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ok, err := DeleteEvidenceBatchObservation(context.Background(), tx, scope, id)
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}
func TestBatchObservationDeletionMatchesLegacyReferencesAndPreservesOtherRecords(t *testing.T) {
	for _, expired := range []bool{false, true} {
		legacy, _ := detailBatchFixture(t)
		_, db := detailBatchFixture(t)
		r := derivedBatchSQLRecord()
		other := batchSQLRecord(2)
		if expired {
			r.ObservationExpiresAt = &r.Observation.IngestedAt
		}
		for _, record := range []EvidenceBatchRecord{r, other} {
			storeLegacyDetailRecord(t, legacy, record)
			if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		if _, err := legacy.conn.ExecContext(context.Background(), "DELETE FROM observations WHERE id=?", r.Observation.ID); err != nil {
			t.Fatal(err)
		}
		if ok, err := deleteObservationFixture(t, db, r.Observation.ScopeID, r.Observation.ID); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if _, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt.Add(-time.Second)); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("deleted original remains readable", err)
		}
		records := batchSQLRecords(t, db)
		expected := derivedBatchSQLRecord()
		expected.Observation = nil
		expected.ObservationExpiresAt = nil
		for j := range expected.Claims {
			expected.Claims[j].Claim.SourceObservationID = ""
		}
		for j := range expected.Links {
			expected.Links[j].EvidenceObservationID = ""
		}
		found := false
		for _, record := range records {
			if record.Observation != nil {
				if !reflect.DeepEqual(record, other) {
					t.Fatal("unrelated evidence changed", record)
				}
			} else {
				found = true
				if !reflect.DeepEqual(record, expected) {
					t.Fatal("retained evidence changed", record, expected)
				}
				for _, claim := range record.Claims {
					var source sql.NullString
					var expiry int64
					if err := legacy.conn.QueryRowContext(context.Background(), "SELECT source_observation_id,expires_at_ns FROM identity_claims WHERE id=?", claim.Claim.ID).Scan(&source, &expiry); err != nil || source.Valid || claim.Claim.SourceObservationID != "" || expiry != claim.ExpiresAt.UnixNano() {
						t.Fatal(claim, source, expiry, err)
					}
				}
				for _, link := range record.Links {
					var evidence sql.NullString
					if err := legacy.conn.QueryRowContext(context.Background(), "SELECT evidence_observation_id FROM device_claim_links WHERE id=?", link.ID).Scan(&evidence); err != nil || evidence.Valid || link.EvidenceObservationID != "" {
						t.Fatal(link, evidence, err)
					}
				}
			}
		}
		otherRead, err := batchSQLRead(t, db, other.Observation.ScopeID, other.Observation.ID, other.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(otherRead, other) {
			t.Fatal("remaining lookup changed", otherRead, err)
		}
		if !found || len(records) != 2 {
			t.Fatal("lost retained record", records)
		}
		if ok, err := deleteObservationFixture(t, db, r.Observation.ScopeID, r.Observation.ID); err != nil || ok {
			t.Fatal("repeat delete", ok, err)
		}
		// Claims, links, original ID expansion and the remaining lookup survive reopen.
		var path string
		if err := db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
			t.Fatal(err)
		}
		uri, err := sqliteFileURI(path)
		if err != nil {
			t.Fatal(err)
		}
		reopened, err := sql.Open("sqlite", uri+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		durable := batchSQLRecords(t, reopened)
		reopened.Close()
		if !reflect.DeepEqual(durable, records) {
			t.Fatal("delete not durable")
		}
	}
}

func TestBatchObservationDeletionSupportsLegacyScopeAndEmptyBatches(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	storeLegacyDetailRecord(t, s, r)
	if ok, err := deleteObservationFixture(t, db, "scope.other", r.Observation.ID); err != nil || ok {
		t.Fatal("wrong scope deleted legacy", ok, err)
	}
	if ok, err := deleteObservationFixture(t, db, r.Observation.ScopeID, r.Observation.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	assertStageTableCount(t, db, "observations", 0)
	assertStageTableCount(t, db, "identity_claims", 2)
	assertStageTableCount(t, db, "device_claim_links", 2)
	empty := detailRecord(2, 0)
	empty.Claims = nil
	empty.Links = nil
	if ok, err := batchSQLAppend(t, db, empty); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := deleteObservationFixture(t, db, "scope.other", empty.Observation.ID); err != nil || ok {
		t.Fatal("wrong scope deleted batch", ok, err)
	}
	if ok, err := deleteObservationFixture(t, db, empty.Observation.ScopeID, empty.Observation.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, table := range []string{"evidence_batches", "evidence_batch_lookup", "evidence_batch_identity_groups", "evidence_batch_identity_routes"} {
		assertStageTableCount(t, db, table, 0)
	}
}

func TestBatchObservationDeletionRollsBackCorruptionAndLateLookupFailure(t *testing.T) {
	for _, mode := range []string{"payload", "claim-bounds", "observation-bounds", "slot", "source", "late-lookup", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			other := detailRecord(2, 0)
			for _, record := range []EvidenceBatchRecord{r, other} {
				if ok, err := batchSQLAppend(t, db, record); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			mutations := map[string]string{"payload": "UPDATE evidence_batches SET data=x'00'", "claim-bounds": "UPDATE evidence_batches SET last_claim_ns=last_claim_ns+1", "observation-bounds": "UPDATE evidence_batches SET last_observation_ns=last_observation_ns+1", "slot": "UPDATE evidence_batch_lookup SET slot=99 WHERE slot=0", "source": "UPDATE evidence_batch_sources SET scope_id='scope.other'", "late-lookup": "CREATE TRIGGER fail_lookup BEFORE INSERT ON evidence_batch_lookup BEGIN SELECT RAISE(ABORT,'injected'); END"}
			if mode == "source" {
				seedScopeAndSensor(t, s, "scope.other", "sensor.other", r.Observation.IngestedAt)
			}
			if sql, ok := mutations[mode]; ok {
				if _, err := db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "duplicate" {
				storeLegacyDetailRecord(t, s, r)
			}
			var before []byte
			if err := db.QueryRow("SELECT data FROM evidence_batches").Scan(&before); err != nil {
				t.Fatal(err)
			}
			scope := r.Observation.ScopeID
			if mode == "source" {
				scope = "scope.other"
			}
			if ok, err := deleteObservationFixture(t, db, scope, r.Observation.ID); err == nil || ok {
				t.Fatal(ok, err)
			}
			var after []byte
			if err := db.QueryRow("SELECT data FROM evidence_batches").Scan(&after); err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed delete changed evidence", err)
			}
			assertStageTableCount(t, db, "evidence_batch_lookup", 2)
		})
	}
}

func TestBatchObservationDeletionOwnerCommitAndForeignKeys(t *testing.T) {
	_, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if ok, err := DeleteEvidenceBatchObservation(context.Background(), tx, r.Observation.ScopeID, r.Observation.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatal("owner rollback changed original", got, err)
	}
	if _, err := DeleteEvidenceBatchObservation(context.Background(), tx, r.Observation.ScopeID, r.Observation.ID); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if ok, err := deleteObservationFixture(t, db, r.Observation.ScopeID, r.Observation.ID); err == nil || ok {
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
	if ok, err := DeleteEvidenceBatchObservation(ctx, tx, r.Observation.ScopeID, r.Observation.ID); !errors.Is(err, context.Canceled) || ok {
		t.Fatal(ok, err)
	}
}

func TestBatchObservationDeletionRemapsRemainingObservationOnlySlots(t *testing.T) {
	_, db := batchSQLFixture(t)
	var records []EvidenceBatchRecord
	for n := 1; n <= 3; n++ {
		r := detailRecord(n, 0)
		r.Claims = nil
		r.Links = nil
		records = append(records, r)
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if ok, err := deleteObservationFixture(t, db, records[1].Observation.ScopeID, records[1].Observation.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, r := range []EvidenceBatchRecord{records[0], records[2]} {
		got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Fatal(got, err)
		}
	}
	assertStageTableCount(t, db, "evidence_batch_lookup", 2)
}
