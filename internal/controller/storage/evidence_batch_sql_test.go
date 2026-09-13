package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func batchSQLFixture(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	s := openTestStore(t)
	seedScopeAndSensor(t, s, "scope.fixture", "sensor.fixture", batchRecordFixture().Observation.IngestedAt)
	// Dedicated pool: never borrow the Store's pinned autocommit connection.
	dsn, err := sqliteFileURI(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn+"?mode=rw&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)&_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec(evidenceBatchSchema); err != nil {
		t.Fatal(err)
	}
	return s, db
}
func batchSQLAppend(t *testing.T, db *sql.DB, r EvidenceBatchRecord) (bool, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	inserted, err := appendEvidenceBatch(context.Background(), tx, r)
	if err != nil {
		return false, err
	}
	return inserted, tx.Commit()
}
func batchSQLRead(t *testing.T, db *sql.DB, scope, id string, now time.Time) (EvidenceBatchRecord, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return readEvidenceBatch(context.Background(), tx, scope, id, now)
}
func batchSQLRecord(n int) EvidenceBatchRecord {
	r := batchRecordFixture()
	r.Observation.ID = fmt.Sprintf("obs.dw.%032x", n)
	r.Observation.SourceKey = fmt.Sprintf("%032x", n)
	for i := range r.Claims {
		r.Claims[i].Claim.ID = fmt.Sprintf("claim.%d.%d", n, i)
		r.Claims[i].Claim.SourceObservationID = r.Observation.ID
		r.Links[i].ID = fmt.Sprintf("link.%d.%d", n, i)
		r.Links[i].ClaimID = r.Claims[i].Claim.ID
		r.Links[i].EvidenceObservationID = r.Observation.ID
	}
	return r
}
func TestEvidenceBatchSQLReplayAndReopen(t *testing.T) {
	s, db := batchSQLFixture(t)
	first := batchSQLRecord(1)
	if ok, err := batchSQLAppend(t, db, first); !ok || err != nil {
		t.Fatal(ok, err)
	}
	replay := batchSQLRecord(2)
	replay.Observation.SourceKey = first.Observation.SourceKey
	*replay.ObservationExpiresAt = replay.ObservationExpiresAt.Add(time.Hour)
	if ok, err := batchSQLAppend(t, db, replay); ok || err != nil {
		t.Fatal("source replay", ok, err)
	}
	collision := batchSQLRecord(1)
	collision.Observation.SourceKey = "different"
	if ok, err := batchSQLAppend(t, db, collision); ok || err == nil {
		t.Fatal("ID collision", ok, err)
	}
	// Full fallback keys must remain distinct from packed canonical keys.
	full := batchRecordFixture()
	full.Observation.SourceKey = "arbitrary.source.key"
	if ok, err := batchSQLAppend(t, db, full); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dsn, _ := sqliteFileURI(s.Path())
	reopened, err := sql.Open("sqlite", dsn+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, r := range []EvidenceBatchRecord{first, full} {
		got, err := batchSQLRead(t, reopened, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Fatal("original evidence changed", err)
		}
	}
	if _, err := batchSQLRead(t, reopened, first.Observation.ScopeID, replay.Observation.ID, first.Observation.IngestedAt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("replay created lookup", err)
	}
}
func TestEvidenceBatchSQLScopeAndExpiry(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	seedScopeAndSensor(t, s, "scope.other", "sensor.other", r.Observation.IngestedAt)
	if ok, err := batchSQLAppend(t, db, r); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := batchSQLRead(t, db, "scope.other", r.Observation.ID, r.Observation.IngestedAt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("scope leaked", err)
	}
	if _, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, *r.ObservationExpiresAt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expiry boundary", err)
	}
	for _, mutate := range []func(*EvidenceBatchRecord){
		func(r *EvidenceBatchRecord) { r.Observation.ScopeID = "scope.other" },
		func(r *EvidenceBatchRecord) { r.Claims[0].Claim.ScopeID = "scope.other" },
		func(r *EvidenceBatchRecord) { r.Links[0].ClaimID = "claim.missing" },
		func(r *EvidenceBatchRecord) { *r.ObservationExpiresAt = time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		bad := batchSQLRecord(2)
		mutate(&bad)
		if ok, err := batchSQLAppend(t, db, bad); ok || err == nil {
			t.Fatal("invalid bundle accepted", ok, err)
		}
	}
}
func TestEvidenceBatchSQLRolloverAndRollback(t *testing.T) {
	_, db := batchSQLFixture(t)
	for n := 1; n <= 101; n++ {
		if ok, err := batchSQLAppend(t, db, batchSQLRecord(n)); !ok || err != nil {
			t.Fatal(n, ok, err)
		}
	}
	var batches int
	if err := db.QueryRow("SELECT count(*) FROM evidence_batches").Scan(&batches); err != nil || batches != 2 {
		t.Fatal(batches, err)
	}
	// Failure occurs after partial payload rewrite. Rollback must restore both
	// tables, including the last acknowledged record and its independent expiry.
	var before []byte
	if err := db.QueryRow("SELECT data FROM evidence_batches ORDER BY id DESC LIMIT 1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_batch_lookup BEFORE INSERT ON evidence_batch_lookup BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if ok, err := batchSQLAppend(t, db, batchSQLRecord(102)); ok || err == nil {
		t.Fatal(ok, err)
	}
	var after []byte
	if err := db.QueryRow("SELECT data FROM evidence_batches ORDER BY id DESC LIMIT 1").Scan(&after); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial rewrite survived rollback", err)
	}
	for n := 1; n <= 101; n++ {
		r := batchSQLRecord(n)
		got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Fatal("rollover lost evidence", n, err)
		}
	}
}
func TestEvidenceBatchSQLCorruptIndexAndPayload(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE evidence_batch_lookup SET slot=99",
		"UPDATE evidence_batch_lookup SET expires_at_ns=expires_at_ns+1",
		"UPDATE evidence_batch_lookup SET source_key=x'00'",
		"UPDATE evidence_batches SET entries=2",
		"UPDATE evidence_batches SET data=x'00'",
	} {
		t.Run(mutation, func(t *testing.T) {
			_, db := batchSQLFixture(t)
			r := batchSQLRecord(1)
			if _, err := batchSQLAppend(t, db, r); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
			if err == nil || !reflect.DeepEqual(got, EvidenceBatchRecord{}) {
				t.Fatal("corruption returned evidence", err)
			}
		})
	}
}
func TestEvidenceBatchSQLByteRollover(t *testing.T) {
	_, db := batchSQLFixture(t)
	for n := 1; n <= 20; n++ {
		r := batchSQLRecord(n)
		r.Observation.Payload = []byte(`{"padding":"` + strings.Repeat("a", 60000) + `"}`)
		if ok, err := batchSQLAppend(t, db, r); !ok || err != nil {
			t.Fatal(n, ok, err)
		}
	}
	var batches int
	if err := db.QueryRow("SELECT count(*) FROM evidence_batches").Scan(&batches); err != nil || batches < 2 {
		t.Fatal("decoded size did not roll over", batches, err)
	}
}
func TestPackedEvidenceKeysAreDisjoint(t *testing.T) {
	values := []string{"obs.dw.0123456789abcdef0123456789abcdef", "obs.dw.0123456789ABCDEF0123456789ABCDEF", "obs.dw.invalid", "0123456789abcdef0123456789abcdef", "obs.fixture"}
	seen := map[string]bool{}
	for _, value := range values {
		key := string(packedEvidenceKey(value, "obs.dw."))
		if seen[key] {
			t.Fatal("collision")
		}
		seen[key] = true
	}
}
func TestEvidenceBatchSchemaNotActivated(t *testing.T) {
	s := openTestStore(t)
	var count int
	if err := s.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_master WHERE name LIKE 'evidence_batch%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("reserved schema activated", count, err)
	}
}

func TestEvidenceBatchTransactionDoesNotAbsorbStoreWrites(t *testing.T) {
	s, db := batchSQLFixture(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	r := batchRecordFixture()
	if ok, err := s.InsertObservation(context.Background(), *r.Observation); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ObservationCount(context.Background()); err != nil || count != 1 {
		t.Fatal("unrelated acknowledged write rolled back", count, err)
	}
}

func TestEvidenceBatchSQLSourcesAndIndependentRetention(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	seedScopeAndSensor(t, s, "scope.other", "sensor.other", r.Observation.IngestedAt)
	other := batchSQLRecord(2)
	other.Observation.SourceKey = r.Observation.SourceKey
	other.Observation.SensorID = "sensor.other"
	other.Observation.ScopeID = "scope.other"
	for i := range other.Claims {
		other.Claims[i].Claim.SourceSensorID = "sensor.other"
		other.Claims[i].Claim.ScopeID = "scope.other"
	}
	stream := batchSQLRecord(3)
	stream.Observation.SourceKey = r.Observation.SourceKey
	stream.Observation.SourceStream = "other.stream"
	// An expired claim remains exact retained evidence in this raw bundle API;
	// the eventual identity projection must apply its own original stored expiry.
	r.Claims[0].ExpiresAt = r.Observation.IngestedAt.Add(-time.Minute)
	for _, record := range []EvidenceBatchRecord{r, other, stream} {
		if ok, err := batchSQLAppend(t, db, record); !ok || err != nil {
			t.Fatal(ok, err)
		}
		got, err := batchSQLRead(t, db, record.Observation.ScopeID, record.Observation.ID, record.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(got, record) {
			t.Fatal("source or retention changed", err)
		}
	}
	var batches int
	if err := db.QueryRow("SELECT count(*) FROM evidence_batches").Scan(&batches); err != nil || batches != 3 {
		t.Fatal("sources shared batch", batches, err)
	}
}

func TestEvidenceBatchSQLRejectsCrossScopePayload(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	r.Claims[0].Claim.ScopeID = "scope.other"
	data, err := EncodeEvidenceBatch([]EvidenceBatchRecord{r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE evidence_batches SET data=?", data); err != nil {
		t.Fatal(err)
	}
	if _, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt); !errors.Is(err, ErrEvidenceBatchData) {
		t.Fatal("cross-scope claim returned", err)
	}
	if ok, err := batchSQLAppend(t, db, batchSQLRecord(2)); ok || !errors.Is(err, ErrEvidenceBatchData) {
		t.Fatal("corrupt partial batch reused", ok, err)
	}
}
