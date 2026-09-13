package storage

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func TestEvidenceBatchReplayAcrossStoredKeyForms(t *testing.T) {
	for _, firstMatches := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching-first", false: "exception-first"}[firstMatches], func(t *testing.T) {
			_, db := batchSQLFixture(t)
			matching, exception := batchSQLRecord(1), batchSQLRecord(2)
			exception.Observation.SourceKey = matching.Observation.SourceKey
			first, replay := matching, exception
			if !firstMatches {
				first, replay = exception, matching
			}
			if ok, err := batchSQLAppend(t, db, first); !ok || err != nil {
				t.Fatal(ok, err)
			}
			if ok, err := batchSQLAppend(t, db, replay); ok || err != nil {
				t.Fatal("cross-form replay accepted", ok, err)
			}
			got, err := batchSQLRead(t, db, first.Observation.ScopeID, first.Observation.ID, first.Observation.IngestedAt)
			if err != nil || !reflect.DeepEqual(got, first) {
				t.Fatal("replay changed original", err)
			}

		})
	}
}
func TestEvidenceBatchReplayConstraintsCoverDirectInserts(t *testing.T) {
	for _, firstMatches := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching-first", false: "exception-first"}[firstMatches], func(t *testing.T) {
			_, db := batchSQLFixture(t)
			first := batchSQLRecord(1)
			if !firstMatches {
				first.Observation.SourceKey = batchSQLRecord(2).Observation.SourceKey
			}
			if _, err := batchSQLAppend(t, db, first); err != nil {
				t.Fatal(err)
			}
			newID := packedEvidenceKey(batchSQLRecord(2).Observation.ID, "obs.dw.")
			var key any = packedEvidenceKey(first.Observation.SourceKey, "")
			if !firstMatches {
				key = nil
			}
			_, err := db.Exec(`INSERT INTO evidence_batch_lookup(id,source_id,source_key,batch_id,slot,expires_at_ns) SELECT ?,source_id,?,batch_id,99,expires_at_ns FROM evidence_batch_lookup LIMIT 1`, newID, key)
			if err == nil {
				t.Fatal("database accepted cross-form duplicate")
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM evidence_batch_lookup").Scan(&count); err != nil || count != 1 {
				t.Fatal("failed insert changed lookup", count, err)
			}
		})
	}
}
func TestEvidenceBatchReplayConstraintsCoverUpdates(t *testing.T) {
	// A matching row and an exceptional row begin with distinct source keys.
	for _, mutation := range []string{"exception-key", "matching-id", "source"} {
		t.Run(mutation, func(t *testing.T) {
			_, db := batchSQLFixture(t)
			first, second := batchSQLRecord(1), batchSQLRecord(2)
			second.Observation.SourceKey = batchSQLRecord(3).Observation.SourceKey
			if mutation == "source" {
				second.Observation.SourceKey = first.Observation.SourceKey
				second.Observation.SourceStream = "other.stream"
			}
			for _, r := range []EvidenceBatchRecord{first, second} {
				if _, err := batchSQLAppend(t, db, r); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch mutation {
			case "exception-key":
				_, err = db.Exec(`UPDATE evidence_batch_lookup SET source_key=? WHERE id=?`, packedEvidenceKey(first.Observation.SourceKey, ""), packedEvidenceKey(second.Observation.ID, "obs.dw."))
			case "matching-id":
				_, err = db.Exec(`UPDATE evidence_batch_lookup SET id=? WHERE id=?`, packedEvidenceKey(second.Observation.SourceKey, ""), packedEvidenceKey(first.Observation.ID, "obs.dw."))
			case "source":
				_, err = db.Exec(`UPDATE evidence_batch_lookup SET source_id=(SELECT source_id FROM evidence_batch_lookup WHERE id=?) WHERE id=?`, packedEvidenceKey(first.Observation.ID, "obs.dw."), packedEvidenceKey(second.Observation.ID, "obs.dw."))
			}
			if err == nil {
				t.Fatal("database accepted duplicate on update")
			}
			for _, r := range []EvidenceBatchRecord{first, second} {
				got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
				if err != nil || !reflect.DeepEqual(got, r) {
					t.Fatal("failed update changed original", err)
				}
			}
		})
	}
}
func TestEvidenceBatchReplayUpdateDoesNotConflictWithItself(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	id := packedEvidenceKey(r.Observation.ID, "obs.dw.")
	for _, key := range []any{id, nil} {
		if _, err := db.Exec(`UPDATE evidence_batch_lookup SET source_key=? WHERE id=?`, key, id); err != nil {
			t.Fatal("same tuple rejected", err)
		}
		if ok, err := batchSQLAppend(t, db, r); ok || err != nil {
			t.Fatal("tuple replay changed", ok, err)
		}
	}
}
func TestEvidenceBatchReplayLookupUsesBothIndexes(t *testing.T) {
	_, db := batchSQLFixture(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM evidence_batch_lookup WHERE id=? AND source_id=? AND source_key IS NULL
 UNION ALL SELECT 1 FROM evidence_batch_lookup WHERE source_id=? AND source_key=? LIMIT 1`, []byte{1}, 1, 1, []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var primary, sparse bool
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN evidence_batch_lookup") {
			t.Fatal("replay scans the lookup table")
		}
		primary = primary || strings.Contains(detail, "PRIMARY KEY")
		sparse = sparse || strings.Contains(detail, "evidence_batch_replay")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !primary || !sparse {
		t.Fatal("replay did not use both bounded index paths")
	}
}
func TestEvidenceBatchSparseReplaySurvivesPruneAndReopen(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	r.Observation.SourceKey = "arbitrary.key"
	r.Claims[0].ExpiresAt = r.Observation.IngestedAt
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	if _, err := batchSQLPrune(t, db, r.Observation.IngestedAt, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dsn, _ := sqliteFileURI(s.Path())
	reopened, err := sql.Open("sqlite", dsn+"?mode=rw&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if ok, err := batchSQLAppend(t, reopened, r); ok || err != nil {
		t.Fatal("reopen lost sparse replay", ok, err)
	}
	tx, err := reopened.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := pruneEvidenceBatches(context.Background(), tx, r.Claims[1].ExpiresAt, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if ok, err := batchSQLAppend(t, reopened, r); !ok || err != nil {
		t.Fatal("expired source key not released", ok, err)
	}
}

func TestEvidenceBatchSparseReplayIndexesOnlyExceptionalKeys(t *testing.T) {
	_, db := batchSQLFixture(t)
	if _, err := batchSQLAppend(t, db, batchSQLRecord(1)); err != nil {
		t.Fatal(err)
	}
	var payload int64
	if err := db.QueryRow(`SELECT sum(payload) FROM dbstat WHERE name='evidence_batch_replay'`).Scan(&payload); err != nil || payload != 0 {
		t.Fatal("matching key duplicated in replay index", payload, err)
	}
	r := batchSQLRecord(2)
	r.Observation.SourceKey = "arbitrary.key"
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT sum(payload) FROM dbstat WHERE name='evidence_batch_replay'`).Scan(&payload); err != nil || payload == 0 {
		t.Fatal("exceptional key absent from replay index", payload, err)
	}
	replay := batchSQLRecord(3)
	replay.Observation.SourceKey = r.Observation.SourceKey
	if ok, err := batchSQLAppend(t, db, replay); ok || err != nil {
		t.Fatal("exceptional key replay accepted", ok, err)
	}
	_, err := db.Exec(`INSERT INTO evidence_batch_lookup(id,source_id,source_key,batch_id,slot,expires_at_ns) SELECT ?,source_id,source_key,batch_id,99,expires_at_ns FROM evidence_batch_lookup WHERE id=?`, packedEvidenceKey(replay.Observation.ID, "obs.dw."), packedEvidenceKey(r.Observation.ID, "obs.dw."))
	if err == nil {
		t.Fatal("database accepted duplicate exceptional key")
	}
}
