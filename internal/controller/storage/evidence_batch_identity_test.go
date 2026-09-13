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

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func batchIdentityRead(t *testing.T, db *sql.DB, now, since, until time.Time) ([]domain.Device, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	return reader.FindRecentDevicesByClaim(context.Background(), "scope.fixture", domain.ClaimMAC, "02-00-00-00-00-01", since, until)
}
func seedBatchDevice(t *testing.T, s *Store, r EvidenceBatchRecord) {
	t.Helper()
	for _, l := range r.Links {
		if err := s.EnsureDevice(context.Background(), domain.Device{ID: l.DeviceID, CreatedAt: r.Claims[0].Claim.ObservedAt.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
}
func shiftBatchClaimTimes(r *EvidenceBatchRecord, at time.Time) {
	for i := range r.Claims {
		r.Claims[i].Claim.ObservedAt = at
		valid := at.Add(time.Minute)
		r.Claims[i].Claim.ValidUntil = &valid
	}
}

func TestBatchIdentityGroupingPreservesOriginalEvidence(t *testing.T) {
	_, db := batchSQLFixture(t)
	records := []EvidenceBatchRecord{batchSQLRecord(1), batchSQLRecord(2), batchSQLRecord(3)}
	for i := range records[1].Links {
		records[1].Links[i].DeviceID = "device.other"
	}
	for _, r := range records {
		if _, err := batchSQLAppend(t, db, r); err != nil {
			t.Fatal(err)
		}
	}
	var groups, batches int
	if err := db.QueryRow("SELECT count(*) FROM evidence_batch_identity_groups").Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM evidence_batches").Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if groups != 2 || batches != 2 {
		t.Fatal("unrelated identities share a batch", groups, batches)
	}
	for _, want := range records {
		got, err := batchSQLRead(t, db, want.Observation.ScopeID, want.Observation.ID, want.Observation.IngestedAt)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("grouping changed evidence", err)
		}
	}
	// Order and duplicate links do not change the normalized lookup dictionary.
	reordered := records[0]
	reordered.Links = append([]domain.DeviceClaimLink(nil), reordered.Links...)
	reordered.Links[0], reordered.Links[1] = reordered.Links[1], reordered.Links[0]
	key, _, _ := evidenceBatchRouting(records[0])
	other, _, _ := evidenceBatchRouting(reordered)
	if string(key) != string(other) {
		t.Fatal("link order changed routing")
	}
}

func TestBatchIdentityQueryDoesNotInventContinuityAcrossGaps(t *testing.T) {
	s, db := batchSQLFixture(t)
	a, b := batchSQLRecord(1), batchSQLRecord(2)
	at := a.Claims[0].Claim.ObservedAt
	seedBatchDevice(t, s, a)
	shiftBatchClaimTimes(&b, at.Add(time.Hour))
	for _, r := range []EvidenceBatchRecord{a, b} {
		if _, err := batchSQLAppend(t, db, r); err != nil {
			t.Fatal(err)
		}
	}
	now := at.Add(time.Minute)
	if got, err := batchIdentityRead(t, db, now, at.Add(30*time.Minute), at.Add(30*time.Minute)); err != nil || len(got) != 0 {
		t.Fatal("bounds fabricated evidence inside a gap", got, err)
	}
	for _, when := range []time.Time{at, at.Add(time.Hour)} {
		if got, err := batchIdentityRead(t, db, now, when, when); err != nil || len(got) != 1 {
			t.Fatal("exact evidence time missing", got, err)
		}
	}
	// Expired observation payloads do not erase independently retained claims.
	if _, err := batchSQLPrune(t, db, *a.ObservationExpiresAt, 100); err != nil {
		t.Fatal(err)
	}
	if got, err := batchIdentityRead(t, db, *a.ObservationExpiresAt, at, at); err != nil || len(got) != 1 {
		t.Fatal("retained identity lost with observation", got, err)
	}
	// The IP claim lives longer, so broad batch expiry still overlaps. The MAC's
	// own exact expiry must exclude it before physical pruning.
	if got, err := batchIdentityRead(t, db, a.Claims[0].ExpiresAt, at, at.Add(time.Hour)); err != nil || len(got) != 0 {
		t.Fatal("one claim borrowed another claim's expiry", got, err)
	}
}

func TestMixedBatchIdentityRetainsAmbiguityAndScope(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	r := batchSQLRecord(1)
	at := r.Claims[0].Claim.ObservedAt
	s.now = func() time.Time { return at }
	seedBatchDevice(t, s, r)
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	// Same device appears in both formats; two more candidates must remain.
	for _, id := range []string{"device.fixture", "device.a", "device.b", "device.c"} {
		if err := s.EnsureDevice(ctx, domain.Device{ID: id, CreatedAt: at.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		c := r.Claims[0].Claim
		c.ID = "legacy." + id
		c.SourceObservationID = ""
		if err := s.InsertIdentityClaim(ctx, c); err != nil {
			t.Fatal(err)
		}
		l := r.Links[0]
		l.ID = "legacy.link." + id
		l.ClaimID = c.ID
		l.DeviceID = id
		l.EvidenceObservationID = ""
		if err := s.CreateDeviceClaimLink(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := batchIdentityRead(t, db, at, at, at)
	if err != nil || len(got) != 3 || got[0].ID != "device.a" || got[1].ID != "device.b" || got[2].ID != "device.c" {
		t.Fatal("mixed ordering/ambiguity", got, err)
	}
	tx, _ := db.BeginTx(ctx, nil)
	defer tx.Rollback()
	reader, _ := NewMixedIdentitySnapshot(tx, at)
	if got, err := reader.FindRecentDevicesByClaim(ctx, "scope.other", domain.ClaimMAC, r.Claims[0].Claim.Value, at, at); err != nil || len(got) != 0 {
		t.Fatal("scope leaked", got, err)
	}
}

func TestBatchIdentityRetentionRekeysAndRemovesDictionaries(t *testing.T) {
	_, db := batchSQLFixture(t)
	a, b := batchSQLRecord(1), batchSQLRecord(2)
	// Both start with the same route set; only a's MAC expires first.
	b.Claims[0].ExpiresAt = b.Claims[0].ExpiresAt.Add(time.Hour)
	for _, r := range []EvidenceBatchRecord{a, b} {
		if _, err := batchSQLAppend(t, db, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := batchSQLPrune(t, db, a.Claims[0].ExpiresAt, 100); err != nil {
		t.Fatal(err)
	}
	var batches, groups int
	if err := db.QueryRow("SELECT count(*) FROM evidence_batches").Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM evidence_batch_identity_groups").Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if batches != 2 || groups != 2 {
		t.Fatal("independent expiry did not split routing", batches, groups)
	}
	// Every rebuilt group must exactly describe all its surviving records.
	tx, _ := db.BeginTx(context.Background(), nil)
	var ids []int64
	rows, err := tx.Query("SELECT id FROM evidence_batches")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		var group, source int64
		var data []byte
		if err := tx.QueryRow("SELECT identity_group,source_id,data FROM evidence_batches WHERE id=?", id).Scan(&group, &source, &data); err != nil {
			t.Fatal(err)
		}
		records, err := DecodeEvidenceBatch(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateEvidenceBatchIdentityGroup(context.Background(), tx, group, source, records); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := batchSQLPrune(t, db, b.Claims[0].ExpiresAt, 100); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"evidence_batches", "evidence_batch_identity_groups", "evidence_batch_identity_routes"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("expired routing retained", table, count, err)
		}
	}
}

func TestBatchIdentityRejectsCorruptRoutesAndRollsBack(t *testing.T) {
	for _, mutation := range []string{"DELETE FROM evidence_batch_identity_routes", "UPDATE evidence_batches SET first_claim_ns=first_claim_ns-1", "UPDATE evidence_batch_identity_routes SET value='02:00:00:00:00:ff'"} {
		t.Run(mutation, func(t *testing.T) {
			_, db := batchSQLFixture(t)
			r := batchSQLRecord(1)
			if _, err := batchSQLAppend(t, db, r); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if ok, err := batchSQLAppend(t, db, batchSQLRecord(2)); err == nil || ok {
				t.Fatal("append repaired corrupt routing", ok, err)
			}
			if _, err := batchSQLPrune(t, db, *r.ObservationExpiresAt, 100); err == nil {
				t.Fatal("prune repaired corrupt routing")
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM evidence_batch_lookup").Scan(&count); err != nil || count != 1 {
				t.Fatal("failed operation escaped rollback", count, err)
			}
		})
	}
}

func TestBatchIdentityQueryUsesIndexAndReturnsNoPartialOnCorruption(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	seedBatchDevice(t, s, r)
	at := r.Claims[0].Claim.ObservedAt
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("EXPLAIN QUERY PLAN "+batchIdentityCandidateSQL, domain.ClaimMAC, r.Claims[0].Claim.Value, r.Observation.ScopeID, at.UnixNano(), at.UnixNano(), at.UnixNano(), at.UnixNano(), "", "", 0, 0, int64(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	var plans []string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	rows.Close()
	if !strings.Contains(strings.Join(plans, "\n"), "evidence_batch_identity_value") {
		t.Fatal("missing value index", plans)
	}
	if _, err := db.Exec("UPDATE evidence_batches SET data=x'00'"); err != nil {
		t.Fatal(err)
	}
	if got, err := batchIdentityRead(t, db, at, at, at); err == nil || got != nil {
		t.Fatal("corrupt batch returned identity", got, err)
	}
	tx, _ := db.BeginTx(context.Background(), nil)
	reader, _ := NewMixedIdentitySnapshot(tx, at)
	tx.Rollback()
	if got, err := reader.FindRecentDevicesByClaim(context.Background(), r.Observation.ScopeID, domain.ClaimMAC, r.Claims[0].Claim.Value, at, at); !errors.Is(err, sql.ErrTxDone) || got != nil {
		t.Fatal("snapshot escaped transaction", got, err)
	}
}

func TestBatchIdentityWorkLimitDoesNotReturnFalseAbsence(t *testing.T) {
	s, db := batchSQLFixture(t)
	at := batchSQLRecord(1).Claims[0].Claim.ObservedAt
	seedBatchDevice(t, s, batchSQLRecord(1))
	for i := 0; i <= evidenceBatchIdentityMaxCandidates; i++ {
		for j := 0; j < 2; j++ {
			r := batchSQLRecord(i*2 + j + 1)
			r.Claims[1].Claim.Value = fmt.Sprintf("192.168.50.%d", i+1)
			shiftBatchClaimTimes(&r, at.Add(time.Duration(j)*time.Hour))
			if _, err := batchSQLAppend(t, db, r); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Every group's broad bounds overlap the gap; none has an exact observation.
	// Exhausting the work budget must fail, never certify absence/uniqueness.
	got, err := batchIdentityRead(t, db, at, at.Add(30*time.Minute), at.Add(30*time.Minute))
	if !errors.Is(err, ErrEvidenceBatchQueryLimit) || got != nil {
		t.Fatal("work limit implied an identity decision", got, err)
	}
}

func TestMixedBatchIdentityCombinesAndDeduplicatesFormats(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	at := r.Claims[0].Claim.ObservedAt
	s.now = func() time.Time { return at }
	seedBatchDevice(t, s, r)
	if _, err := batchSQLAppend(t, db, r); err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{"device.other", "device.fixture"} {
		if err := s.EnsureDevice(context.Background(), domain.Device{ID: device, CreatedAt: at.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		c := r.Claims[0].Claim
		c.ID = "claim.legacy." + device
		c.SourceObservationID = ""
		if err := s.InsertIdentityClaim(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		l := r.Links[0]
		l.ID = "link.legacy." + device
		l.ClaimID = c.ID
		l.DeviceID = device
		l.EvidenceObservationID = ""
		if err := s.CreateDeviceClaimLink(context.Background(), l); err != nil {
			t.Fatal(err)
		}
		got, err := batchIdentityRead(t, db, at, at, at)
		if err != nil || len(got) != 2 || got[0].ID != "device.fixture" || got[1].ID != "device.other" {
			t.Fatal("mixed evidence lost ambiguity or duplicated a device", got, err)
		}
	}
}
