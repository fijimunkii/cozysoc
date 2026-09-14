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

func readMixedDevices(t *testing.T, db *sql.DB, now time.Time, q DeviceEvidenceQuery) (DeviceEvidencePage, error) {
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
	return reader.ListDeviceEvidence(context.Background(), q)
}
func deviceListRecord(n int, device string, offset time.Duration) EvidenceBatchRecord {
	r := detailRecord(n, offset)
	for j := range r.Links {
		r.Links[j].DeviceID = device
	}
	return r
}

func TestMixedDeviceListMatchesLegacyPaginationHistoryAndExpiry(t *testing.T) {
	legacy, _ := batchSQLFixture(t)
	mixed, db := batchSQLFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for _, s := range []*Store{legacy, mixed} {
		seedScopeAndSensor(t, s, "scope.other", "sensor.other", base)
		for _, id := range []string{"device.a", "device.b", "device.c", "device.d", "device.e", "device.f", "device.g", "device.other"} {
			d := domain.Device{ID: id, UserLabel: id, CreatedAt: base}
			if id == "device.g" {
				at := base.Add(10 * time.Minute)
				d.RetiredAt = &at
			}
			if err := s.CreateDevice(context.Background(), d); err != nil {
				t.Fatal(err)
			}
		}
	}
	records := []EvidenceBatchRecord{
		deviceListRecord(1, "device.a", 0), deviceListRecord(2, "device.a", 20*time.Minute),
		deviceListRecord(3, "device.b", 0), deviceListRecord(4, "device.c", 0),
		deviceListRecord(5, "device.d", 20*time.Minute), deviceListRecord(6, "device.e", 0),
		deviceListRecord(7, "device.f", 0), deviceListRecord(8, "device.g", 0), deviceListRecord(9, "device.other", 0),
	}
	for j := range records[6].Links {
		records[6].Links[j].ValidFrom = base.Add(time.Hour)
		records[6].Links[j].ValidUntil = nil
	}
	records[5].Claims[0].ExpiresAt = base
	records[8].Observation.ScopeID = "scope.other"
	records[8].Observation.SensorID = "sensor.other"
	for j := range records[8].Claims {
		records[8].Claims[j].Claim.ScopeID = "scope.other"
		records[8].Claims[j].Claim.SourceSensorID = "sensor.other"
	}
	for n, r := range records {
		storeLegacyDetailRecord(t, legacy, r)
		if n == 0 || n == 3 {
			storeLegacyDetailRecord(t, mixed, r)
		} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	for _, elapsed := range []time.Duration{0, 2 * time.Hour, 3 * time.Hour} {
		for _, asof := range []time.Duration{0, 30 * time.Minute, 90 * time.Minute} {
			for _, limit := range []int{1, 2, 5, 200} {
				for _, after := range []string{"", "device.b", "device.z"} {
					t.Run(fmt.Sprintf("now=%s/asof=%s/limit=%d/after=%s", elapsed, asof, limit, after), func(t *testing.T) {
						now := base.Add(elapsed)
						legacy.now = func() time.Time { return now }
						q := DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: base.Add(asof), Limit: limit, AfterID: after}
						want, werr := legacy.ListDeviceEvidence(context.Background(), q)
						got, gerr := readMixedDevices(t, db, now, q)
						if werr != nil || gerr != nil || !reflect.DeepEqual(got, want) {
							t.Fatalf("mixed=%+v (%v) legacy=%+v (%v)", got, gerr, want, werr)
						}
					})
				}
			}
		}
	}
	// Retained claims remain list evidence after original observations are pruned.
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
	q := DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: base.Add(30 * time.Minute)}
	want, werr := legacy.ListDeviceEvidence(context.Background(), q)
	got, gerr := readMixedDevices(t, db, now, q)
	if werr != nil || gerr != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, gerr, want, werr)
	}
}

func TestMixedDeviceListFindsOriginalLastSeenBelowFutureBatchBound(t *testing.T) {
	_, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	r := detailRecord(1, 0)
	r.Claims[1].Claim.ObservedAt = base.Add(time.Hour)
	r.Claims[1].Claim.ValidUntil = nil
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	newer := detailRecord(2, 20*time.Minute)
	newer.Claims[0].Claim.Value = "02:00:00:00:00:02"
	if ok, err := batchSQLAppend(t, db, newer); err != nil || !ok {
		t.Fatal(ok, err)
	}
	got, err := readMixedDevices(t, db, base, DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: base.Add(30 * time.Minute)})
	if err != nil || len(got.Devices) != 1 || !got.Devices[0].LastSeen.Equal(base.Add(20*time.Minute)) {
		t.Fatal("routing bound fabricated last-seen", got, err)
	}
}

func TestMixedDeviceListCorruptionDoesNotReturnLegacyOnlyPage(t *testing.T) {
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
			got, err := readMixedDevices(t, db, r.Claims[0].Claim.ObservedAt, DeviceEvidenceQuery{ScopeID: "scope.fixture"})
			if err == nil || !reflect.DeepEqual(got, DeviceEvidencePage{}) {
				t.Fatal("partial page escaped error", got, err)
			}
		})
	}
}

func TestMixedDeviceListBoundsIneligibleWorkWithoutSkippingEvidence(t *testing.T) {
	s, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	storeLegacyDetailRecord(t, s, detailRecord(9999, -time.Minute))
	for n := 1; n <= evidenceBatchDeviceListMaxCandidates+1; n++ {
		r := detailRecord(n, 0)
		r.Claims[0].Claim.Value = fmt.Sprintf("02:00:00:01:%02x:%02x", n>>8, n&255)
		r.Claims[0].ExpiresAt = base
		r.Claims[1].Claim.ObservedAt = base.Add(time.Hour)
		r.Claims[1].Claim.ValidUntil = nil
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	got, err := readMixedDevices(t, db, base, DeviceEvidenceQuery{ScopeID: "scope.fixture"})
	if !errors.Is(err, ErrEvidenceBatchQueryLimit) || !reflect.DeepEqual(got, DeviceEvidencePage{}) {
		t.Fatal("work limit claimed completeness", got, err)
	}
	scope, err := readMixedScopeDevices(t, db, base, DeviceQuery{ScopeID: "scope.fixture"})
	if !errors.Is(err, ErrEvidenceBatchQueryLimit) || !reflect.DeepEqual(scope, DevicePage{}) {
		t.Fatal("scope work limit claimed completeness", scope, err)
	}
}

func TestMixedDeviceListSnapshotRetirementAndClosedTransaction(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	base := r.Claims[0].Claim.ObservedAt
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
	q := DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: base}
	got, err := reader.ListDeviceEvidence(context.Background(), q)
	if err != nil || len(got.Devices) != 1 || got.Devices[0].Device.UserLabel != "staged" {
		t.Fatal(got, err)
	}
	q.AsOf = base.Add(time.Nanosecond)
	if got, err := reader.ListDeviceEvidence(context.Background(), q); err != nil || len(got.Devices) != 0 {
		t.Fatal(got, err)
	}
	var label string
	if err := s.conn.QueryRowContext(context.Background(), "SELECT user_label FROM devices").Scan(&label); err != nil || label != "Fixture device" {
		t.Fatal(label, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListDeviceEvidence(context.Background(), q); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}

func TestMixedDeviceListStopsAtLegacyPageBoundary(t *testing.T) {
	s, db := batchSQLFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for n, id := range []string{"device.a", "device.b", "device.z"} {
		if err := s.CreateDevice(context.Background(), domain.Device{ID: id, CreatedAt: base}); err != nil {
			t.Fatal(err)
		}
		r := deviceListRecord(n+1, id, 0)
		if n < 2 {
			storeLegacyDetailRecord(t, s, r)
		} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if _, err := db.Exec("UPDATE evidence_batches SET data=x'00'"); err != nil {
		t.Fatal(err)
	}
	q := DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: base, Limit: 1}
	page, err := readMixedDevices(t, db, base, q)
	if err != nil || len(page.Devices) != 1 || page.Devices[0].Device.ID != "device.a" || page.NextID != "device.a" {
		t.Fatal(page, err)
	}
	q.AfterID = page.NextID
	if page, err := readMixedDevices(t, db, base, q); err == nil || !reflect.DeepEqual(page, DeviceEvidencePage{}) {
		t.Fatal("selected corruption was hidden", page, err)
	}
}

func TestMixedDeviceListUsesDeviceRoutingIndex(t *testing.T) {
	_, db := detailBatchFixture(t)
	at := batchRecordFixture().Claims[0].Claim.ObservedAt.UnixNano()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+batchDeviceListCandidateSQL, "scope.fixture", at, at, at, "", "", "", false, int64(0), int64(0), int64(0), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found, deviceRange, batchLookup := false, false, false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		found = found || strings.Contains(detail, "SEARCH r USING COVERING INDEX evidence_batch_identity_device")
		deviceRange = deviceRange || strings.Contains(detail, "SEARCH d USING INDEX")
		batchLookup = batchLookup || strings.Contains(detail, "SEARCH b USING INDEX evidence_batches_identity_group")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found || !deviceRange || !batchLookup {
		t.Fatal("list query did not use device routing index")
	}
}
