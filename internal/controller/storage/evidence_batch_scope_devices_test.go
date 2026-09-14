package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func readMixedScopeDevices(t *testing.T, db *sql.DB, now time.Time, q DeviceQuery) (DevicePage, error) {
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
	return reader.ListDevicesForScope(context.Background(), q)
}

func TestMixedScopeDevicesMatchLegacyValidityPaginationAndPruning(t *testing.T) {
	for _, allBatch := range []bool{false, true} {
		legacy, _ := batchSQLFixture(t)
		mixed, db := batchSQLFixture(t)
		base := batchRecordFixture().Claims[0].Claim.ObservedAt
		ids := []string{"device.a", "device.b", "device.c", "device.d", "device.e", "device.f", "device.g", "device.other"}
		for _, s := range []*Store{legacy, mixed} {
			seedScopeAndSensor(t, s, "scope.other", "sensor.other", base)
			for _, id := range ids {
				d := domain.Device{ID: id, CreatedAt: base}
				if id == "device.g" {
					at := base.Add(10 * time.Minute)
					d.RetiredAt = &at
				}
				if err := s.CreateDevice(context.Background(), d); err != nil {
					t.Fatal(err)
				}
			}
		}
		records := make([]EvidenceBatchRecord, 0, len(ids)+1)
		for n, id := range ids {
			r := deviceListRecord(n+1, id, 0)
			for j := range r.Claims {
				switch id {
				case "device.b":
					r.Claims[j].Claim.ValidUntil = nil
					r.Links[j].ValidUntil = nil
				case "device.c":
					r.Claims[j].Claim.ValidUntil = nil // Only the link ends.
				case "device.d":
					r.Links[j].ValidUntil = nil // Only the claim ends.
				case "device.e":
					r.Claims[j].Claim.ValidUntil = nil
					r.Links[j].ValidFrom = base.Add(20 * time.Minute)
					r.Links[j].ValidUntil = nil
				case "device.f":
					r.Claims[j].ExpiresAt = base.Add(10 * time.Minute)
					r.Claims[j].Claim.ValidUntil = nil
					r.Links[j].ValidUntil = nil
				case "device.other":
					r.Claims[j].Claim.ScopeID = "scope.other"
					r.Claims[j].Claim.SourceSensorID = "sensor.other"
				}
			}
			if id == "device.other" {
				r.Observation.ScopeID = "scope.other"
				r.Observation.SensorID = "sensor.other"
			}
			records = append(records, r)
		}
		// A device in both formats must remain unique; its second interval must not
		// fill the gap between observations.
		records = append(records, deviceListRecord(99, "device.a", 20*time.Minute))
		for n, r := range records {
			storeLegacyDetailRecord(t, legacy, r)
			if !allBatch && n%3 == 0 {
				storeLegacyDetailRecord(t, mixed, r)
			} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		compare := func(now time.Time) {
			legacy.now = func() time.Time { return now }
			for _, elapsed := range []time.Duration{-time.Nanosecond, 0, 10 * time.Minute, 10*time.Minute + time.Nanosecond, 15 * time.Minute, 20 * time.Minute, 30 * time.Minute, 30*time.Minute + time.Nanosecond} {
				for _, limit := range []int{1, 3, 200} {
					for _, after := range []string{"", "device.b", "device.z"} {
						q := DeviceQuery{ScopeID: "scope.fixture", AsOf: base.Add(elapsed), Limit: limit, AfterID: after}
						want, we := legacy.ListDevicesForScope(context.Background(), q)
						got, ge := readMixedScopeDevices(t, db, now, q)
						if we != nil || ge != nil || !reflect.DeepEqual(got, want) {
							t.Fatalf("allBatch=%v now=%v elapsed=%v q=%+v got=%+v (%v) want=%+v (%v)", allBatch, now, elapsed, q, got, ge, want, we)
						}
					}
				}
			}
		}
		compare(base)
		compare(base.Add(10 * time.Minute))
		compare(base.Add(90 * time.Minute))
		// Pruning observations must leave independently retained valid claims usable.
		now := base.Add(90 * time.Minute)
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

func TestMixedScopeDevicesRejectSelectedCorruption(t *testing.T) {
	for _, mutation := range []string{"UPDATE evidence_batches SET data=x'00'", "UPDATE evidence_batches SET last_claim_ns=last_claim_ns+1", "UPDATE evidence_batch_lookup SET slot=99", "DELETE FROM evidence_batch_identity_routes WHERE kind='ipv4'", "DROP TABLE evidence_batch_lookup"} {
		t.Run(mutation, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			base := r.Claims[0].Claim.ObservedAt
			storeLegacyDetailRecord(t, s, detailRecord(2, -time.Minute))
			if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if got, err := readMixedScopeDevices(t, db, base, DeviceQuery{ScopeID: "scope.fixture"}); err == nil || !reflect.DeepEqual(got, DevicePage{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMixedScopeDevicesContinuePastInvalidNewerBatch(t *testing.T) {
	_, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	older := detailRecord(1, 0)
	for j := range older.Claims {
		older.Claims[j].Claim.ValidUntil = nil
		older.Links[j].ValidUntil = nil
	}
	newer := detailRecord(2, 20*time.Minute)
	newer.Claims[0].Claim.Value = "02:00:00:00:00:02"
	for _, r := range []EvidenceBatchRecord{older, newer} {
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	got, err := readMixedScopeDevices(t, db, base, DeviceQuery{ScopeID: "scope.fixture", AsOf: base.Add(time.Hour)})
	if err != nil || len(got.Devices) != 1 {
		t.Fatal(got, err)
	}
}

func TestMixedScopeDevicesSnapshotAndCancellation(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	base := r.Claims[0].Claim.ObservedAt
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := NewMixedIdentitySnapshot(tx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE devices SET user_label='staged',retired_at_ns=?", base.UnixNano()); err != nil {
		t.Fatal(err)
	}
	q := DeviceQuery{ScopeID: "scope.fixture", AsOf: base}
	if got, err := reader.ListDevicesForScope(context.Background(), q); err != nil || len(got.Devices) != 1 || got.Devices[0].UserLabel != "staged" {
		t.Fatal(got, err)
	}
	q.AsOf = base.Add(time.Nanosecond)
	if got, err := reader.ListDevicesForScope(context.Background(), q); err != nil || len(got.Devices) != 0 {
		t.Fatal(got, err)
	}
	var label string
	if err := s.conn.QueryRowContext(context.Background(), "SELECT user_label FROM devices").Scan(&label); err != nil || label != "Fixture device" {
		t.Fatal(label, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := reader.ListDevicesForScope(ctx, q); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, DevicePage{}) {
		t.Fatal(got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListDevicesForScope(context.Background(), q); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}
