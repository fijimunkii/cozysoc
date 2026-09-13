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

func detailBatchFixture(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	s, db := batchSQLFixture(t)
	if err := s.CreateDevice(context.Background(), domain.Device{ID: "device.fixture", UserLabel: "Fixture device", CreatedAt: batchRecordFixture().Claims[0].Claim.ObservedAt}); err != nil {
		t.Fatal(err)
	}
	return s, db
}
func detailRecord(n int, offset time.Duration) EvidenceBatchRecord {
	r := batchSQLRecord(n)
	at := r.Claims[0].Claim.ObservedAt.Add(offset)
	r.Observation.SourceTime = &at
	r.Observation.IngestedAt = at.Add(time.Minute)
	for j := range r.Claims {
		r.Claims[j].Claim.ObservedAt = at
		valid := at.Add(10 * time.Minute)
		r.Claims[j].Claim.ValidUntil = &valid
		r.Links[j].ValidFrom = at
		r.Links[j].ValidUntil = &valid
		r.Links[j].CreatedAt = at
	}
	return r
}
func storeLegacyDetailRecord(t *testing.T, s *Store, r EvidenceBatchRecord) {
	t.Helper()
	ctx := context.Background()
	if ok, err := s.InsertObservation(ctx, *r.Observation); err != nil || !ok {
		t.Fatal(ok, err)
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
}
func readMixedDetail(t *testing.T, db *sql.DB, now time.Time, q DeviceEvidenceDetailQuery) (DeviceEvidenceDetail, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	return reader.GetDeviceEvidenceDetail(context.Background(), q)
}

func TestMixedDeviceDetailMatchesLegacyAcrossTimeExpiryAndLimits(t *testing.T) {
	legacy, _ := detailBatchFixture(t)
	mixed, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for n := 1; n <= 4; n++ {
		r := detailRecord(n, time.Duration(n-1)*20*time.Minute)
		if n == 2 {
			r.Claims[0].Claim.Value = "02:AA:00:00:00:01"
		}
		if n == 3 {
			r.Links[0].ValidFrom = base.Add(90 * time.Minute)
			r.Links[0].ValidUntil = nil
		}
		storeLegacyDetailRecord(t, legacy, r)
		if n == 1 {
			storeLegacyDetailRecord(t, mixed, r)
		} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	for _, elapsed := range []time.Duration{0, 30 * time.Minute, 70 * time.Minute, 2 * time.Hour, 3 * time.Hour} {
		for _, asof := range []time.Duration{0, 30 * time.Minute, 70 * time.Minute} {
			for _, limit := range []int{1, 2, 100} {
				t.Run(fmt.Sprintf("now=%s/asof=%s/limit=%d", elapsed, asof, limit), func(t *testing.T) {
					now := base.Add(elapsed)
					legacy.now = func() time.Time { return now }
					q := DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: base.Add(asof), Limit: limit}
					want, werr := legacy.GetDeviceEvidenceDetail(context.Background(), q)
					got, gerr := readMixedDetail(t, db, now, q)
					if (werr != nil) != (gerr != nil) || !reflect.DeepEqual(got, want) {
						t.Fatalf("mixed=%+v (%v) legacy=%+v (%v)", got, gerr, want, werr)
					}
				})
			}
		}
	}
	q := DeviceEvidenceDetailQuery{ScopeID: "scope.other", DeviceID: "device.fixture", AsOf: base}
	if got, err := readMixedDetail(t, db, base, q); !errors.Is(err, ErrDeviceEvidenceNotFound) || !reflect.DeepEqual(got, DeviceEvidenceDetail{}) {
		t.Fatal(got, err)
	}
	// Expired source observations are pruned, but independently retained claims
	// still produce identical history without resurrecting provenance.
	now := base.Add(90 * time.Minute)
	legacy.now = func() time.Time { return now }
	if _, err := legacy.PruneExpired(context.Background(), now, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := mixed.PruneExpired(context.Background(), now, 100); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pruneEvidenceBatches(context.Background(), tx, now, 100); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	q = DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: base.Add(70 * time.Minute)}
	want, werr := legacy.GetDeviceEvidenceDetail(context.Background(), q)
	got, gerr := readMixedDetail(t, db, now, q)
	if werr != nil || gerr != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, gerr, want, werr)
	}
}

func TestMixedDeviceDetailRetirementSnapshotAndClosedTransaction(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	at := r.Claims[0].Claim.ObservedAt
	q := DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: at}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := NewMixedIdentitySnapshot(tx, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE devices SET user_label='staged label',retired_at_ns=?", at.UnixNano()); err != nil {
		t.Fatal(err)
	}
	got, err := reader.GetDeviceEvidenceDetail(context.Background(), q)
	if err != nil || got.Summary.Device.UserLabel != "staged label" {
		t.Fatal(got, err)
	}
	q.AsOf = at.Add(time.Nanosecond)
	if _, err := reader.GetDeviceEvidenceDetail(context.Background(), q); !errors.Is(err, ErrDeviceEvidenceNotFound) {
		t.Fatal(err)
	}
	var label string
	if err := s.conn.QueryRowContext(context.Background(), "SELECT user_label FROM devices").Scan(&label); err != nil || label != "Fixture device" {
		t.Fatal("snapshot wrote outside transaction", label, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetDeviceEvidenceDetail(context.Background(), q); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if got, err := readMixedDetail(t, db, at, q); err != nil || got.Summary.Device.RetiredAt != nil {
		t.Fatal(got, err)
	}
}

func TestMixedDeviceDetailRejectsSelectedCorruptionAndSchemaErrors(t *testing.T) {
	for _, mutation := range []string{"UPDATE evidence_batches SET data=x'00'", "UPDATE evidence_batches SET last_claim_ns=last_claim_ns+1", "DELETE FROM evidence_batch_identity_routes WHERE kind='ipv4'", "UPDATE evidence_batch_lookup SET slot=99", "DROP TABLE evidence_batch_lookup"} {
		t.Run(mutation, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			storeLegacyDetailRecord(t, s, detailRecord(2, -time.Minute))
			if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			got, err := readMixedDetail(t, db, r.Claims[0].Claim.ObservedAt, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: r.Claims[0].Claim.ObservedAt})
			if err == nil || !reflect.DeepEqual(got, DeviceEvidenceDetail{}) {
				t.Fatal("partial detail escaped error", got, err)
			}
		})
	}
}

func TestMixedDeviceDetailBoundsWorkAndUsesDeviceIndex(t *testing.T) {
	s, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	storeLegacyDetailRecord(t, s, detailRecord(999, -time.Minute))
	for n := 1; n <= 101; n++ {
		r := detailRecord(n, 0)
		r.Claims[0].Claim.Value = fmt.Sprintf("02:00:00:00:01:%02x", n)
		for j := range r.Links {
			r.Links[j].ValidFrom = base.Add(time.Hour)
			r.Links[j].ValidUntil = nil
		}
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	q := DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: base}
	if got, err := readMixedDetail(t, db, base, q); !errors.Is(err, ErrEvidenceBatchQueryLimit) || !reflect.DeepEqual(got, DeviceEvidenceDetail{}) {
		t.Fatal("work limit returned false completeness", got, err)
	}
	rows, err := db.Query("EXPLAIN QUERY PLAN "+batchDeviceDetailCandidateSQL, q.DeviceID, q.ScopeID, base.UnixNano(), base.UnixNano(), base.UnixNano(), true, int64(1<<63-1), int64(1<<63-1), int64(1<<63-1))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(detail, "evidence_batch_identity_device")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("device lookup did not use its index")
	}
}

func TestMixedDeviceDetailOrdersTiesAndStopsAfterVerifiedOlderBounds(t *testing.T) {
	for _, ties := range []bool{true, false} {
		t.Run(fmt.Sprint(ties), func(t *testing.T) {
			_, db := detailBatchFixture(t)
			base := batchRecordFixture().Claims[0].Claim.ObservedAt
			total := 3
			if !ties {
				total = 101
			}
			for n := 1; n <= total; n++ {
				offset := -time.Duration(n-1) * time.Minute
				if ties {
					offset = 0
				}
				r := detailRecord(n, offset)
				r.Claims[0].Claim.Value = fmt.Sprintf("02:00:00:00:01:%02x", n)
				if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			got, err := readMixedDetail(t, db, base, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: base, Limit: 1})
			if err != nil || len(got.Evidence) != 1 || !got.Truncated || got.Evidence[0].Value != "02:00:00:00:01:01" || !got.Summary.LastSeen.Equal(base) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMixedDeviceDetailRejectsDuplicateSelectedLinkIDs(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	old := detailRecord(2, -time.Minute)
	old.Links[0].ID = r.Links[0].ID
	storeLegacyDetailRecord(t, s, old)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	at := r.Claims[0].Claim.ObservedAt
	if got, err := readMixedDetail(t, db, at, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: at}); err == nil || !reflect.DeepEqual(got, DeviceEvidenceDetail{}) {
		t.Fatal(got, err)
	}
}
