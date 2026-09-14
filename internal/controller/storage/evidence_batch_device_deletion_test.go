package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func deleteDeviceFixture(t *testing.T, db *sql.DB, id string, limit int) (bool, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var ok bool
	if limit == evidenceBatchDeviceDeleteMaxCandidates {
		ok, err = DeleteEvidenceBatchDevice(context.Background(), tx, id)
	} else {
		ok, err = deleteEvidenceBatchDevice(context.Background(), tx, id, limit)
	}
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}
func TestBatchDeviceDeletionPreservesOriginalEvidenceAcrossScopes(t *testing.T) {
	for _, prune := range []bool{false, true} {
		legacy, _ := batchSQLFixture(t)
		mixed, db := batchSQLFixture(t)
		base := batchRecordFixture().Claims[0].Claim.ObservedAt
		for _, s := range []*Store{legacy, mixed} {
			seedScopeAndSensor(t, s, "scope.other", "sensor.other", base)
			for _, id := range []string{"device.target", "device.keep"} {
				if err := s.CreateDevice(context.Background(), domain.Device{ID: id, CreatedAt: base}); err != nil {
					t.Fatal(err)
				}
			}
		}
		records := []EvidenceBatchRecord{deviceListRecord(1, "device.target", 0), deviceListRecord(2, "device.target", 0), deviceListRecord(3, "device.target", 0), deviceListRecord(4, "device.keep", 0), deviceListRecord(5, "device.target", 0)}
		for j := range records[0].Claims {
			c, l, _ := derivedEvidenceBatchIDs(records[0], j)
			records[0].Claims[j].Claim.ID = c
			records[0].Links[j].ID = l
			records[0].Links[j].ClaimID = c
		}
		keep := records[1].Links[0]
		keep.ID = "link.keep"
		keep.DeviceID = "device.keep"
		records[1].Links = append(records[1].Links, keep)
		records[2].Observation.ScopeID = "scope.other"
		records[2].Observation.SensorID = "sensor.other"
		for j := range records[2].Claims {
			records[2].Claims[j].Claim.ScopeID = "scope.other"
			records[2].Claims[j].Claim.SourceSensorID = "sensor.other"
		}
		for n, r := range records {
			storeLegacyDetailRecord(t, legacy, r)
			if n == 4 {
				storeLegacyDetailRecord(t, mixed, r)
			} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		if prune {
			at := *records[0].ObservationExpiresAt
			for _, s := range []*Store{legacy, mixed} {
				if _, err := s.PruneExpired(context.Background(), at, 100); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := batchSQLPrune(t, db, at, 100); err != nil {
				t.Fatal(err)
			}
		}
		before := batchSQLRecords(t, db)
		if _, err := legacy.conn.ExecContext(context.Background(), "DELETE FROM devices WHERE id='device.target'"); err != nil {
			t.Fatal(err)
		}
		if ok, err := deleteDeviceFixture(t, db, "device.target", 1024); err != nil || !ok {
			t.Fatal(ok, err)
		}
		for j := range before {
			links := make([]domain.DeviceClaimLink, 0)
			for _, l := range before[j].Links {
				if l.DeviceID != "device.target" {
					links = append(links, l)
				}
			}
			before[j].Links = links
		}
		after := batchSQLRecords(t, db)
		// Empty link arrays and nil are equivalent in the codec. Compare exact retained
		// evidence separately from ordering/group placement after rewriting.
		order := func(r []EvidenceBatchRecord) {
			sort.Slice(r, func(i, j int) bool { return r[i].Claims[0].Claim.ID < r[j].Claims[0].Claim.ID })
			for j := range r {
				if len(r[j].Links) == 0 {
					r[j].Links = nil
				}
			}
		}
		order(before)
		order(after)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("device deletion changed original observations or claims", before, after)
		}
		var legacyLinks int
		if err := legacy.conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM device_claim_links").Scan(&legacyLinks); err != nil {
			t.Fatal(err)
		}
		var mixedLinks int
		if err := db.QueryRow("SELECT COUNT(*) FROM device_claim_links").Scan(&mixedLinks); err != nil {
			t.Fatal(err)
		}
		for _, r := range after {
			mixedLinks += len(r.Links)
		}
		if mixedLinks != legacyLinks {
			t.Fatal(mixedLinks, legacyLinks)
		}
		for _, table := range []string{"devices", "evidence_batch_identity_routes"} {
			var n int
			column := "device_id"
			if table == "devices" {
				column = "id"
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE " + column + "='device.target'").Scan(&n); err != nil || n != 0 {
				t.Fatal(table, n, err)
			}
		}
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query("SELECT id,source_id,identity_group FROM evidence_batches")
		if err != nil {
			t.Fatal(err)
		}
		type batchID struct{ id, source, group int64 }
		var ids []batchID
		for rows.Next() {
			var b batchID
			if err := rows.Scan(&b.id, &b.source, &b.group); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, b)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, b := range ids {
			var data []byte
			if err := tx.QueryRow("SELECT data FROM evidence_batches WHERE id=?", b.id).Scan(&data); err != nil {
				t.Fatal(err)
			}
			r, err := DecodeEvidenceBatch(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateEvidenceBatchBounds(context.Background(), tx, b.id, r); err != nil {
				t.Fatal(err)
			}
			if err := validateEvidenceBatchLookups(context.Background(), tx, b.id, b.source, r); err != nil {
				t.Fatal(err)
			}
			if err := validateEvidenceBatchIdentityGroup(context.Background(), tx, b.group, b.source, r); err != nil {
				t.Fatal(err)
			}
		}
		tx.Rollback()
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
		order(durable)
		if !reflect.DeepEqual(durable, after) {
			t.Fatal("delete did not survive reopen")
		}
		if ok, err := deleteDeviceFixture(t, db, "device.target", 1024); err != nil || ok {
			t.Fatal("repeat delete", ok, err)
		}
	}
}

func TestBatchDeviceDeletionRollsBackOnBudgetCorruptionAndLateFailure(t *testing.T) {
	for _, mode := range []string{"budget", "payload", "bounds", "lookup", "late-delete"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			for n := 1; n <= 2; n++ {
				r := detailRecord(n, 0)
				if n == 2 {
					r.Claims[0].Claim.Value = "02:00:00:00:00:02"
				}
				if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			mutations := map[string]string{"payload": "UPDATE evidence_batches SET data=x'00' WHERE id=(SELECT MAX(id) FROM evidence_batches)", "bounds": "UPDATE evidence_batches SET last_observation_ns=last_observation_ns+1 WHERE id=(SELECT MAX(id) FROM evidence_batches)", "lookup": "UPDATE evidence_batch_lookup SET slot=99 WHERE batch_id=(SELECT MAX(id) FROM evidence_batches)", "late-delete": "CREATE TRIGGER fail_device_delete BEFORE DELETE ON devices BEGIN SELECT RAISE(ABORT,'injected'); END"}
			if sql, ok := mutations[mode]; ok {
				if _, err := db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			var before string
			if err := db.QueryRow("SELECT group_concat(hex(data),'|') FROM (SELECT data FROM evidence_batches ORDER BY id)").Scan(&before); err != nil {
				t.Fatal(err)
			}
			limit := 1024
			if mode == "budget" {
				limit = 1
			}
			if ok, err := deleteDeviceFixture(t, db, "device.fixture", limit); err == nil || ok || (mode == "budget" && !errors.Is(err, ErrEvidenceBatchQueryLimit)) {
				t.Fatal(ok, err)
			}
			var after string
			if err := db.QueryRow("SELECT group_concat(hex(data),'|') FROM (SELECT data FROM evidence_batches ORDER BY id)").Scan(&after); err != nil || before != after {
				t.Fatal("partial rewrite committed", err)
			}
			assertStageTableCount(t, db, "devices", 1)
			var label string
			if err := s.conn.QueryRowContext(context.Background(), "SELECT user_label FROM devices").Scan(&label); err != nil || label != "Fixture device" {
				t.Fatal(label, err)
			}
		})
	}
}

func TestBatchDeviceDeletionRequiresForeignKeysAndUsesRoutingIndex(t *testing.T) {
	_, db := detailBatchFixture(t)
	rows, err := db.Query("EXPLAIN QUERY PLAN "+batchDeviceDeleteCandidateSQL, "device.fixture", true, int64(0))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(detail, "SEARCH r USING COVERING INDEX evidence_batch_identity_device")
		if strings.Contains(detail, "SCAN b") {
			t.Fatal(detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !found {
		t.Fatal("device routing index not used")
	}
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if ok, err := deleteDeviceFixture(t, db, "device.fixture", 1024); err == nil || ok {
		t.Fatal(ok, err)
	}
	assertStageTableCount(t, db, "devices", 1)
}

func TestBatchDeviceDeletionRequiresOwnerCommit(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if ok, err := DeleteEvidenceBatchDevice(context.Background(), tx, "device.fixture"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM devices").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := s.conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM devices").Scan(&count); err != nil || count != 1 {
		t.Fatal("uncommitted deletion leaked", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteEvidenceBatchDevice(context.Background(), tx, "device.fixture"); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatal("rollback changed evidence", got, err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := DeleteEvidenceBatchDevice(ctx, tx, "device.fixture"); !errors.Is(err, context.Canceled) || ok {
		t.Fatal(ok, err)
	}
}

func TestBatchDeviceDeletionGuardRejectsDirectSQL(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.conn.ExecContext(context.Background(), "DELETE FROM devices WHERE id=?", "device.fixture"); err == nil {
		t.Fatal("direct delete bypassed batch link cleanup")
	}
	assertStageTableCount(t, db, "devices", 1)
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM devices WHERE id=?", "device.fixture"); err == nil {
		t.Fatal("disabled foreign keys bypassed delete guard")
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if ok, err := deleteDeviceFixture(t, db, "device.fixture", 1024); err != nil || !ok {
		t.Fatal(ok, err)
	}
}
