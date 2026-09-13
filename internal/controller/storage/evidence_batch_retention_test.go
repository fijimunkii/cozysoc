package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func batchSQLPrune(t *testing.T, db *sql.DB, now time.Time, limit int) (evidenceBatchPruneCounts, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	counts, err := pruneEvidenceBatches(context.Background(), tx, now, limit)
	if err != nil {
		return counts, err
	}
	return counts, tx.Commit()
}
func batchSQLRecords(t *testing.T, db *sql.DB) []EvidenceBatchRecord {
	t.Helper()
	rows, err := db.Query("SELECT entries,next_expiry_ns,data FROM evidence_batches ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var all []EvidenceBatchRecord
	for rows.Next() {
		var count int
		var expiry int64
		var data []byte
		if err := rows.Scan(&count, &expiry, &data); err != nil {
			t.Fatal(err)
		}
		records, err := DecodeEvidenceBatch(data)
		if err != nil || count != len(records) || expiry != nextEvidenceBatchExpiry(records) {
			t.Fatal("invalid retained batch", err)
		}
		all = append(all, records...)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all
}
func derivedBatchSQLRecord() EvidenceBatchRecord {
	r := batchSQLRecord(1)
	for i := range r.Claims {
		c, l, _ := derivedEvidenceBatchIDs(r, i)
		r.Claims[i].Claim.ID = c
		r.Links[i].ID = l
		r.Links[i].ClaimID = c
	}
	return r
}
func TestEvidenceBatchPruneObservationBeforeClaims(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := derivedBatchSQLRecord()
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	counts, err := batchSQLPrune(t, db, *r.ObservationExpiresAt, 1)
	if err != nil || counts != (evidenceBatchPruneCounts{Batches: 1, Observations: 1}) {
		t.Fatal(counts, err)
	}
	expected := derivedBatchSQLRecord()
	expected.Observation = nil
	expected.ObservationExpiresAt = nil
	for i := range expected.Claims {
		expected.Claims[i].Claim.SourceObservationID = ""
		expected.Links[i].EvidenceObservationID = ""
	}
	if got := batchSQLRecords(t, db); !reflect.DeepEqual(got, []EvidenceBatchRecord{expected}) {
		t.Fatal("surviving claims/links changed")
	}
	dsn, _ := sqliteFileURI(s.Path())
	reopened, err := sql.Open("sqlite", dsn+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	gotRetained := batchSQLRecords(t, reopened)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotRetained, []EvidenceBatchRecord{expected}) {
		t.Fatal("pruned evidence did not survive reopen")
	}
	if _, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, *r.ObservationExpiresAt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired lookup remains", err)
	}
	// A partial batch containing retained-only evidence can accept new evidence.
	fresh := batchSQLRecord(2)
	*fresh.ObservationExpiresAt = r.Claims[1].ExpiresAt.Add(time.Hour)
	for i := range fresh.Claims {
		fresh.Claims[i].ExpiresAt = fresh.ObservationExpiresAt.Add(time.Hour)
	}
	if ok, err := batchSQLAppend(t, db, fresh); !ok || err != nil {
		t.Fatal("append after prune", ok, err)
	}
	counts, err = batchSQLPrune(t, db, r.Claims[0].ExpiresAt, 1)
	if err != nil || counts != (evidenceBatchPruneCounts{Batches: 1, Claims: 1, Links: 1}) {
		t.Fatal(counts, err)
	}
	expected.Claims = expected.Claims[1:]
	expected.Links = expected.Links[1:]
	if got := batchSQLRecords(t, db); !reflect.DeepEqual(got, []EvidenceBatchRecord{expected, fresh}) {
		t.Fatal("independent claim expiry changed survivor")
	}
	counts, err = batchSQLPrune(t, db, r.Claims[1].ExpiresAt, 1)
	if err != nil || counts != (evidenceBatchPruneCounts{Batches: 1, Claims: 1, Links: 1}) {
		t.Fatal(counts, err)
	}
	// Removing the first record compacts the surviving lookup's slot from 1 to 0.
	got, err := batchSQLRead(t, db, fresh.Observation.ScopeID, fresh.Observation.ID, r.Claims[1].ExpiresAt)
	if err != nil || !reflect.DeepEqual(got, fresh) {
		t.Fatal("lookup not remapped", err)
	}
	counts, err = batchSQLPrune(t, db, fresh.Claims[0].ExpiresAt, 1)
	if err != nil || counts != (evidenceBatchPruneCounts{Batches: 1, Observations: 1, Claims: 2, Links: 2}) {
		t.Fatal(counts, err)
	}
	if len(batchSQLRecords(t, db)) != 0 {
		t.Fatal("empty batch retained")
	}
}
func TestEvidenceBatchPruneClaimsBeforeObservation(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	at := r.Observation.IngestedAt.Add(time.Minute)
	r.Claims[0].ExpiresAt = at
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	if c, err := batchSQLPrune(t, db, at.Add(-time.Nanosecond), 1); err != nil || c.Batches != 0 {
		t.Fatal("early expiry", c, err)
	}
	if c, err := batchSQLPrune(t, db, at, 1); err != nil || c != (evidenceBatchPruneCounts{Batches: 1, Claims: 1, Links: 1}) {
		t.Fatal(c, err)
	}
	expected := r
	expected.Claims = r.Claims[1:]
	expected.Links = r.Links[1:]
	got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, at)
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatal("live observation changed", err)
	}
	if ok, err := batchSQLAppend(t, db, r); ok || err != nil {
		t.Fatal("pruning renewed source replay", ok, err)
	}
}
func TestEvidenceBatchPruneRollbackAndCorruptLookup(t *testing.T) {
	for _, mutation := range []string{
		`CREATE TRIGGER fail_prune BEFORE INSERT ON evidence_batch_lookup BEGIN SELECT RAISE(ABORT,'injected'); END`,
		`UPDATE evidence_batch_lookup SET slot=99`,
		`DELETE FROM evidence_batch_lookup`,
		`PRAGMA ignore_check_constraints=ON; UPDATE evidence_batch_lookup SET source_key=zeroblob(514)`,
	} {
		t.Run(mutation, func(t *testing.T) {
			_, db := batchSQLFixture(t)
			r := batchSQLRecord(1)
			at := r.Observation.IngestedAt.Add(time.Minute)
			r.Claims[0].ExpiresAt = at
			if _, err := batchSQLAppend(t, db, r); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			var before []byte
			var expiry int64
			if err := db.QueryRow("SELECT data,next_expiry_ns FROM evidence_batches").Scan(&before, &expiry); err != nil {
				t.Fatal(err)
			}
			if counts, err := batchSQLPrune(t, db, at, 1); err == nil || counts != (evidenceBatchPruneCounts{}) {
				t.Fatal("failed prune reported counts", counts, err)
			}
			var after []byte
			var afterExpiry int64
			if err := db.QueryRow("SELECT data,next_expiry_ns FROM evidence_batches").Scan(&after, &afterExpiry); err != nil || !reflect.DeepEqual(before, after) || expiry != afterExpiry {
				t.Fatal("failed prune changed batch", err)
			}
		})
	}
}
func TestEvidenceBatchPruneBoundsAndCancellation(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	at := r.Claims[1].ExpiresAt
	for i := 1; i <= 3; i++ {
		r := batchSQLRecord(i)
		r.Observation.SourceStream = strings.Repeat("x", i)
		if _, err := batchSQLAppend(t, db, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, -1, evidenceBatchMaxPruneBatches + 1} {
		if c, err := batchSQLPrune(t, db, at, limit); err == nil || c.Batches != 0 {
			t.Fatal(c, err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, err := pruneEvidenceBatches(ctx, tx, at, 1); err == nil || c.Batches != 0 {
		t.Fatal(c, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if c, err := batchSQLPrune(t, db, at, 1); err != nil || c.Batches != 1 {
			t.Fatal(c, err)
		}
	}
	if c, err := batchSQLPrune(t, db, at, 1); err != nil || c.Batches != 0 {
		t.Fatal(c, err)
	}
}
func TestRetainedEvidencePartitionIsLossless(t *testing.T) {
	// Exercise fallback repartitioning directly, including payloads near the
	// per-record limit. Every resulting frame must satisfy both codec limits.
	var input []EvidenceBatchRecord
	for i := 1; i <= 20; i++ {
		r := batchSQLRecord(i)
		r.Observation.Payload = []byte(`{"padding":"` + strings.Repeat("a", 60000) + `"}`)
		input = append(input, r)
	}
	groups, err := partitionRetainedEvidence(input)
	if err != nil || len(groups) < 2 {
		t.Fatal(len(groups), err)
	}
	var got []EvidenceBatchRecord
	for _, group := range groups {
		data, err := EncodeEvidenceBatch(group)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeEvidenceBatch(data)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, decoded...)
	}
	if !reflect.DeepEqual(input, got) {
		t.Fatal("partition changed original evidence")
	}
}
func TestEvidenceBatchPruningMatchesLegacyForeignKeys(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	ctx := context.Background()
	if err := s.CreateDevice(ctx, domain.Device{ID: r.Links[0].DeviceID, CreatedAt: r.Observation.IngestedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertObservation(ctx, *r.Observation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(ctx, "UPDATE observations SET expires_at_ns=? WHERE id=?", r.ObservationExpiresAt.UnixNano(), r.Observation.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Claims {
		if err := s.InsertIdentityClaim(ctx, c.Claim); err != nil {
			t.Fatal(err)
		}
		if _, err := s.conn.ExecContext(ctx, "UPDATE identity_claims SET expires_at_ns=? WHERE id=?", c.ExpiresAt.UnixNano(), c.Claim.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range r.Links {
		if err := s.CreateDeviceClaimLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{*r.ObservationExpiresAt, r.Claims[0].ExpiresAt, r.Claims[1].ExpiresAt} {
		legacy, err := s.PruneExpired(ctx, at, 100)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := batchSQLPrune(t, db, at, 1)
		if err != nil {
			t.Fatal(err)
		}
		if int64(batch.Observations) != legacy["observations"] || int64(batch.Claims) != legacy["identity_claims"] {
			t.Fatal("retention counts differ", batch, legacy)
		}
		records := batchSQLRecords(t, db)
		var claims, links int
		for _, record := range records {
			for _, c := range record.Claims {
				claims++
				var source sql.NullString
				if err := s.conn.QueryRowContext(ctx, "SELECT source_observation_id FROM identity_claims WHERE id=?", c.Claim.ID).Scan(&source); err != nil || source.String != c.Claim.SourceObservationID {
					t.Fatal("claim reference differs", err)
				}
			}
			for _, l := range record.Links {
				links++
				var source sql.NullString
				if err := s.conn.QueryRowContext(ctx, "SELECT evidence_observation_id FROM device_claim_links WHERE id=?", l.ID).Scan(&source); err != nil || source.String != l.EvidenceObservationID {
					t.Fatal("link reference differs", err)
				}
			}
		}
		var legacyClaims, legacyLinks int
		if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM identity_claims").Scan(&legacyClaims); err != nil {
			t.Fatal(err)
		}
		if err := s.conn.QueryRowContext(ctx, "SELECT count(*) FROM device_claim_links").Scan(&legacyLinks); err != nil {
			t.Fatal(err)
		}
		if claims != legacyClaims || links != legacyLinks {
			t.Fatal("retained rows differ")
		}
	}
}

func TestEvidenceBatchPruneRollsBackEarlierBatches(t *testing.T) {
	_, db := batchSQLFixture(t)
	var records []EvidenceBatchRecord
	for i := 1; i <= 2; i++ {
		r := batchSQLRecord(i)
		r.Observation.SourceStream = strings.Repeat("x", i)
		if _, err := batchSQLAppend(t, db, r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_second_prune BEFORE UPDATE ON evidence_batches WHEN OLD.id=2 BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	counts, err := batchSQLPrune(t, db, *records[0].ObservationExpiresAt, 2)
	if err == nil || counts != (evidenceBatchPruneCounts{}) {
		t.Fatal(counts, err)
	}
	if got := batchSQLRecords(t, db); !reflect.DeepEqual(got, records) {
		t.Fatal("earlier batch survived failed transaction")
	}
	for _, r := range records {
		got, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Fatal("earlier lookup lost", err)
		}
	}
}

func TestEvidenceBatchPruneMakesPagesReusable(t *testing.T) {
	_, db := batchSQLFixture(t)
	// Distinct streams create enough independent pages to observe deletion reuse.
	for i := 1; i <= 30; i++ {
		r := batchSQLRecord(i)
		r.Observation.SourceStream = strings.Repeat("x", i)
		if _, err := batchSQLAppend(t, db, r); err != nil {
			t.Fatal(err)
		}
	}
	var before, after int
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&before); err != nil {
		t.Fatal(err)
	}
	at := batchSQLRecord(1).Claims[1].ExpiresAt
	if c, err := batchSQLPrune(t, db, at, 30); err != nil || c.Batches != 30 {
		t.Fatal(c, err)
	}
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&after); err != nil || after <= before {
		t.Fatal("deleted pages not reusable", before, after, err)
	}
}
