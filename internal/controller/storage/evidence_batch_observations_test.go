package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func readMixedObservations(t *testing.T, db *sql.DB, now time.Time, q ObservationQuery) (ObservationPage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	return reader.ListObservations(ctx, q)
}

func TestMixedObservationsMatchOriginalHistoryAndPagination(t *testing.T) {
	for _, allBatch := range []bool{false, true} {
		legacy, _ := detailBatchFixture(t)
		mixed, db := detailBatchFixture(t)
		base := batchRecordFixture().Claims[0].Claim.ObservedAt
		for _, s := range []*Store{legacy, mixed} {
			seedScopeAndSensor(t, s, "scope.other", "sensor.other", base)
			if err := s.CreateSensor(context.Background(), domain.Sensor{ID: "sensor.second", ScopeID: "scope.fixture", Kind: "desktop-discovery", Ownership: "builtin", RegisteredAt: base, Metadata: []byte(`{"fixture":true}`)}); err != nil {
				t.Fatal(err)
			}
		}
		for n := 1; n <= 12; n++ {
			r := detailRecord(n, time.Duration(n)*time.Hour)
			// Claims lie outside the query window, and batches have overlapping ingestion
			// intervals. Neither claim times nor physical append order can define history.
			r.Observation.IngestedAt = base.Add(time.Duration(n%4) * time.Minute)
			source := base.Add(-24 * time.Hour).In(time.FixedZone("fixture", -4*60*60))
			r.Observation.SourceTime = &source
			r.Observation.Payload = []byte(" {\n  \"fixture\": true\n} ")
			if n%3 == 0 {
				r.Observation.Kind = "fixture-status"
			}
			if n%4 == 0 {
				r.Observation.SensorID = "sensor.second"
				for j := range r.Claims {
					r.Claims[j].Claim.SourceSensorID = "sensor.second"
				}
			}
			if n == 12 {
				r.Observation.ScopeID = "scope.other"
				r.Observation.SensorID = "sensor.other"
				for j := range r.Claims {
					r.Claims[j].Claim.ScopeID = "scope.other"
					r.Claims[j].Claim.SourceSensorID = "sensor.other"
				}
			}
			if n%2 == 0 {
				expiry := base.Add(30 * time.Minute)
				r.ObservationExpiresAt = &expiry
			}
			storeLegacyDetailRecord(t, legacy, r)
			if !allBatch && n%3 == 1 {
				storeLegacyDetailRecord(t, mixed, r)
			} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		compare := func(now time.Time) {
			legacy.now = func() time.Time { return now }
			for _, sensor := range []string{"", "sensor.fixture", "sensor.second"} {
				for _, kind := range []string{"", "device-neighbor-seen", "fixture-status", "absent"} {
					for _, limit := range []int{1, 3, 200} {
						q := ObservationQuery{ScopeID: "scope.fixture", SensorID: sensor, Kind: kind, Since: base, Until: base.Add(3 * time.Minute), Limit: limit}
						seen := map[string]bool{}
						for pageN := 0; pageN < 20; pageN++ {
							want, we := legacy.ListObservations(context.Background(), q)
							got, ge := readMixedObservations(t, db, now, q)
							if we != nil || ge != nil || !reflect.DeepEqual(got, want) {
								t.Fatalf("batch=%v now=%v q=%+v got=%+v (%v) want=%+v (%v)", allBatch, now, q, got, ge, want, we)
							}
							for _, o := range got.Observations {
								if seen[o.ID] {
									t.Fatal("duplicate paginated observation", o.ID)
								}
								seen[o.ID] = true
							}
							if got.Next == nil {
								break
							}
							q.Before = got.Next
							if pageN == 19 {
								t.Fatal("pagination did not end")
							}
						}
					}
				}
			}
		}
		compare(base)
		compare(base.Add(30 * time.Minute))
		compare(base.Add(90 * time.Minute))
		now := base.Add(30 * time.Minute)
		for _, s := range []*Store{legacy, mixed} {
			if _, err := s.PruneExpired(context.Background(), now, 100); err != nil {
				t.Fatal(err)
			}
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
		compare(now)
	}
}

func TestMixedObservationsRejectCorruptionAndDuplicateOriginals(t *testing.T) {
	for _, mode := range []string{"payload", "bounds", "lookup", "routes", "schema", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			base := r.Observation.IngestedAt
			if mode != "duplicate" {
				storeLegacyDetailRecord(t, s, detailRecord(2, 0))
			}
			if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if mode == "duplicate" {
				// Simulate a legacy writer introducing corruption after the batch.
				storeLegacyDetailRecord(t, s, r)
			}
			mutations := map[string]string{"payload": "UPDATE evidence_batches SET data=x'00'", "bounds": "UPDATE evidence_batches SET last_observation_ns=last_observation_ns+1", "lookup": "UPDATE evidence_batch_lookup SET slot=99", "routes": "DELETE FROM evidence_batch_identity_routes WHERE kind='ipv4'", "schema": "DROP INDEX evidence_batches_observation_time"}
			if mutation, ok := mutations[mode]; ok {
				if _, err := db.Exec(mutation); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := readMixedObservations(t, db, base, ObservationQuery{ScopeID: "scope.fixture", Since: base, Until: base.Add(time.Hour)}); err == nil || !reflect.DeepEqual(got, ObservationPage{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMixedObservationsProcessEqualTimeBatchesBeforeTruncating(t *testing.T) {
	legacy, _ := detailBatchFixture(t)
	_, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for n := 1; n <= 5; n++ {
		r := detailRecord(n, 0)
		r.Claims[0].Claim.Value = fmt.Sprintf("02:00:00:00:00:%02x", n)
		storeLegacyDetailRecord(t, legacy, r)
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	legacy.now = func() time.Time { return base }
	q := ObservationQuery{ScopeID: "scope.fixture", Since: base, Until: base.Add(time.Hour), Limit: 1}
	for n := 0; n < 5; n++ {
		want, we := legacy.ListObservations(context.Background(), q)
		got, ge := readMixedObservations(t, db, base, q)
		if we != nil || ge != nil || !reflect.DeepEqual(got, want) || len(got.Observations) != 1 {
			t.Fatal(got, ge, want, we)
		}
		q.Before = got.Next
	}
}

func TestMixedObservationBoundsRemainAtomicAndPruneToNoObservations(t *testing.T) {
	_, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	base := r.Observation.IngestedAt
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pruneEvidenceBatches(context.Background(), tx, *r.ObservationExpiresAt, 100); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var first, last, expiry int64
	if err := tx.QueryRow("SELECT first_observation_ns,last_observation_ns,last_observation_expiry_ns FROM evidence_batches").Scan(&first, &last, &expiry); err != nil || first != 0 || last != 0 || expiry != 0 {
		t.Fatal(first, last, expiry, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := readMixedObservations(t, db, base, ObservationQuery{ScopeID: "scope.fixture", Until: base}); err != nil || len(got.Observations) != 1 {
		t.Fatal(got, err)
	}
	// Append and prune must reject corrupted persisted bounds instead of repairing
	// them implicitly and making an incomplete history look trustworthy.
	if _, err := db.Exec("UPDATE evidence_batches SET first_observation_ns=first_observation_ns-1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := batchSQLAppend(t, db, detailRecord(2, 0)); err == nil || ok {
		t.Fatal(ok, err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := pruneEvidenceBatches(context.Background(), tx, *r.ObservationExpiresAt, 100); err == nil {
		t.Fatal("prune accepted bad bounds")
	}
}

func TestMixedObservationsUseIngestionIndex(t *testing.T) {
	_, db := detailBatchFixture(t)
	base := batchRecordFixture().Observation.IngestedAt.UnixNano()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+batchObservationCandidateSQL, base, int64(math.MaxInt64), base, base, "scope.fixture", "", "", true, int64(0), int64(0), int64(0))
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
		t.Log(detail)
		found = found || strings.Contains(detail, "SEARCH b USING INDEX evidence_batches_observation_time")
		if strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal("history requires sort of all batches", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("ingestion index not used")
	}
}

func TestMixedObservationsSnapshotCancellationAndClosedTransaction(t *testing.T) {
	_, db := detailBatchFixture(t)
	db.SetMaxOpenConns(2)
	r := detailRecord(1, 0)
	base := r.Observation.IngestedAt
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if ok, err := appendEvidenceBatch(context.Background(), tx, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	reader, err := NewMixedIdentitySnapshot(tx, base)
	if err != nil {
		t.Fatal(err)
	}
	q := ObservationQuery{ScopeID: "scope.fixture", Until: base}
	if got, err := reader.ListObservations(context.Background(), q); err != nil || len(got.Observations) != 1 {
		t.Fatal(got, err)
	}
	if got, err := readMixedObservations(t, db, base, q); err != nil || len(got.Observations) != 0 {
		t.Fatal("staged history leaked", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := reader.ListObservations(ctx, q); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ObservationPage{}) {
		t.Fatal(got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListObservations(context.Background(), q); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}

func TestMixedObservationsBoundLegacyProjection(t *testing.T) {
	for _, mode := range []string{"metadata", "payload", "page-bytes"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			base := r.Observation.IngestedAt
			storeLegacyDetailRecord(t, s, r)
			switch mode {
			case "metadata":
				if _, err := db.Exec("UPDATE observations SET source_key=?", strings.Repeat("x", 513)); err != nil {
					t.Fatal(err)
				}
			case "payload":
				if _, err := db.Exec("UPDATE observations SET payload=?", strings.Repeat(" ", 1<<20)+"{}"); err != nil {
					t.Fatal(err)
				}
			case "page-bytes":
				for n := 2; n <= 17; n++ {
					storeLegacyDetailRecord(t, s, detailRecord(n, 0))
				}
				if _, err := db.Exec("UPDATE observations SET payload=?", strings.Repeat(" ", (1<<20)-2)+"{}"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readMixedObservations(t, db, base, ObservationQuery{ScopeID: "scope.fixture", Until: base})
			if err == nil || !reflect.DeepEqual(got, ObservationPage{}) {
				t.Fatal(got, err)
			}
			if mode == "page-bytes" && !errors.Is(err, ErrEvidenceBatchQueryLimit) {
				t.Fatal(err)
			}
		})
	}
}

func TestBatchObservationTimesRejectSQLOverflow(t *testing.T) {
	for _, sourceOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(sourceOnly), func(t *testing.T) {
			_, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			outside := time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC)
			if sourceOnly {
				r.Observation.SourceTime = &outside
			} else {
				r.Observation.IngestedAt = outside
			}
			if ok, err := batchSQLAppend(t, db, r); err == nil || ok {
				t.Fatal("wrapped original timestamp", ok, err)
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM evidence_batches").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
