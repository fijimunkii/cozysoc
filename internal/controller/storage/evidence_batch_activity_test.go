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

func activityRecord(n int, device string, offset time.Duration, address string, kind domain.ClaimKind) EvidenceBatchRecord {
	r := deviceListRecord(n, device, offset)
	expiry := batchRecordFixture().Claims[0].Claim.ObservedAt.Add(30 * 24 * time.Hour)
	r.ObservationExpiresAt = &expiry
	for j := range r.Claims {
		r.Claims[j].ExpiresAt = expiry
	}
	r.Claims[1].Claim.Kind = kind
	r.Claims[1].Claim.Value = address
	return r
}
func readMixedActivity(t *testing.T, db *sql.DB, now time.Time, q DeviceActivityQuery) (DeviceActivityPage, error) {
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
	return reader.ListDeviceActivity(context.Background(), q)
}

func TestMixedActivityMatchesLegacyBeforeClassifyingChanges(t *testing.T) {
	for _, allBatch := range []bool{false, true} {
		t.Run(fmt.Sprint(allBatch), func(t *testing.T) {
			legacy, _ := batchSQLFixture(t)
			mixed, db := batchSQLFixture(t)
			base := batchRecordFixture().Claims[0].Claim.ObservedAt
			for _, s := range []*Store{legacy, mixed} {
				seedScopeAndSensor(t, s, "scope.other", "sensor.other", base)
				for _, d := range []domain.Device{{ID: "device.one", UserLabel: "Fixture one", CreatedAt: base.Add(-6 * 24 * time.Hour)}, {ID: "device.two", CreatedAt: base.Add(time.Hour)}, {ID: "device.three", CreatedAt: base.Add(-8 * 24 * time.Hour)}} {
					if err := s.CreateDevice(context.Background(), d); err != nil {
						t.Fatal(err)
					}
				}
			}
			records := []EvidenceBatchRecord{
				activityRecord(1, "device.one", -6*24*time.Hour, "192.168.50.10", domain.ClaimIPv4),
				activityRecord(2, "device.one", -25*time.Hour, "192.168.50.10", domain.ClaimIPv4),
				activityRecord(3, "device.one", 0, "192.168.50.20", domain.ClaimIPv4),
				activityRecord(4, "device.one", time.Hour, "192.168.50.20", domain.ClaimIPv4),
				activityRecord(5, "device.one", 2*time.Hour, "2001:db8::1", domain.ClaimIPv6),
				activityRecord(6, "device.one", 3*time.Hour, "192.168.50.10", domain.ClaimIPv4),
				activityRecord(7, "device.two", time.Hour, "192.168.50.30", domain.ClaimIPv4),
				activityRecord(8, "device.two", 3*time.Hour, "192.168.50.30", domain.ClaimIPv4),
				activityRecord(9, "device.three", -8*24*time.Hour, "192.168.50.40", domain.ClaimIPv4),
				activityRecord(10, "device.three", time.Hour, "192.168.50.50", domain.ClaimIPv4),
				activityRecord(11, "device.one", 3*time.Hour, "192.168.60.10", domain.ClaimIPv4),
			}
			records[10].Observation.ScopeID = "scope.other"
			records[10].Observation.SensorID = "sensor.other"
			for j := range records[10].Claims {
				records[10].Claims[j].Claim.ScopeID = "scope.other"
				records[10].Claims[j].Claim.SourceSensorID = "sensor.other"
			}
			for index, r := range records {
				storeLegacyDetailRecord(t, legacy, r)
				if !allBatch && index%2 == 0 {
					storeLegacyDetailRecord(t, mixed, r)
				} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			for _, nowOffset := range []time.Duration{4 * time.Hour, 31 * 24 * time.Hour} {
				for _, asof := range []time.Duration{30 * time.Minute, 3 * time.Hour, 4 * time.Hour, 24 * time.Hour, 25 * time.Hour} {
					for _, limit := range []int{1, 3, 100} {
						now := base.Add(nowOffset)
						legacy.now = func() time.Time { return now }
						q := DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: base.Add(asof), Limit: limit}
						want, werr := legacy.ListDeviceActivity(context.Background(), q)
						got, gerr := readMixedActivity(t, db, now, q)
						if werr != nil || gerr != nil || !reflect.DeepEqual(got, want) {
							t.Fatalf("now=%s asof=%s limit=%d mixed=%+v (%v) legacy=%+v (%v)", nowOffset, asof, limit, got, gerr, want, werr)
						}
					}
				}
			}
			page, err := readMixedActivity(t, db, base.Add(4*time.Hour), DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: base.Add(4 * time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range page.Items {
				if item.ID == records[2].Observation.ID {
					found = true
					if item.Kind != DeviceActivityAddressChanged || item.PreviousAddress != "192.168.50.10" {
						t.Fatal("cross-format/outside-display predecessor lost", item)
					}
				}
				if item.DeviceID == "device.three" && item.Kind != DeviceActivityObserved {
					t.Fatal("predecessor outside seven days used", item)
				}
			}
			if !found {
				t.Fatal("missing cross-format address change")
			}
		})
	}
}

func TestMixedActivityIndependentExpiryAndPruning(t *testing.T) {
	legacy, _ := detailBatchFixture(t)
	mixed, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	records := []EvidenceBatchRecord{
		activityRecord(1, "device.fixture", 0, "192.168.50.10", domain.ClaimIPv4),
		activityRecord(2, "device.fixture", time.Hour, "192.168.50.20", domain.ClaimIPv4),
		activityRecord(3, "device.fixture", 2*time.Hour, "192.168.50.30", domain.ClaimIPv4),
		activityRecord(4, "device.fixture", 3*time.Hour, "192.168.50.40", domain.ClaimIPv4),
	}
	expiry := base.Add(4 * time.Hour)
	records[1].ObservationExpiresAt = &expiry
	records[2].Claims[0].ExpiresAt = expiry
	for j := range records[3].Links {
		records[3].Links[j].ValidFrom = base.Add(5 * time.Hour)
		records[3].Links[j].ValidUntil = nil
	}
	for n, r := range records {
		storeLegacyDetailRecord(t, legacy, r)
		if n == 0 {
			storeLegacyDetailRecord(t, mixed, r)
		} else if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	now := expiry
	legacy.now = func() time.Time { return now }
	q := DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: base.Add(4 * time.Hour)}
	for pass := 0; pass < 2; pass++ {
		if pass > 0 {
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
		}
		want, werr := legacy.ListDeviceActivity(context.Background(), q)
		got, gerr := readMixedActivity(t, db, now, q)
		if werr != nil || gerr != nil || !reflect.DeepEqual(got, want) || len(got.Items) != 1 || got.Items[0].ID != records[0].Observation.ID {
			t.Fatal(got, gerr, want, werr)
		}
	}
}

func TestMixedActivityRejectsCorruptionAndDuplicateOriginals(t *testing.T) {
	for _, mode := range []string{"payload", "bounds", "lookup", "routes", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			base := batchRecordFixture().Claims[0].Claim.ObservedAt
			r := activityRecord(1, "device.fixture", 0, "192.168.50.10", domain.ClaimIPv4)
			if mode != "duplicate" {
				storeLegacyDetailRecord(t, s, activityRecord(2, "device.fixture", time.Minute, "192.168.50.20", domain.ClaimIPv4))
			}
			if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if mode == "duplicate" {
				// Simulate a legacy writer introducing corruption after the batch.
				storeLegacyDetailRecord(t, s, r)
			}
			mutations := map[string]string{"payload": "UPDATE evidence_batches SET data=x'00'", "bounds": "UPDATE evidence_batches SET last_claim_ns=last_claim_ns+1", "lookup": "UPDATE evidence_batch_lookup SET slot=99", "routes": "DELETE FROM evidence_batch_identity_routes WHERE kind='ipv4'"}
			if sql, ok := mutations[mode]; ok {
				if _, err := db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readMixedActivity(t, db, base, DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: base.Add(time.Hour)})
			if err == nil || !reflect.DeepEqual(got, DeviceActivityPage{}) {
				t.Fatal("partial activity escaped failure", got, err)
			}
		})
	}
}

func TestMixedActivityBudgetsRejectIncompleteHistory(t *testing.T) {
	s, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	storeLegacyDetailRecord(t, s, activityRecord(1, "device.fixture", 0, "192.168.50.10", domain.ClaimIPv4))
	if ok, err := batchSQLAppend(t, db, activityRecord(2, "device.fixture", time.Minute, "192.168.50.20", domain.ClaimIPv4)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, budget := range []activityQueryBudget{{devices: 0, batches: 10, rows: 10}, {devices: 10, batches: 0, rows: 10}, {devices: 10, batches: 10, rows: 1}} {
		tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := readMixedDeviceActivity(context.Background(), tx, base, DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: base.Add(time.Hour)}, &budget)
		tx.Rollback()
		if !errors.Is(err, ErrEvidenceBatchQueryLimit) || !reflect.DeepEqual(got, DeviceActivityPage{}) {
			t.Fatal(got, err)
		}
	}
}

func TestMixedActivitySnapshotRetirementAndClosedTransaction(t *testing.T) {
	s, db := detailBatchFixture(t)
	r := activityRecord(1, "device.fixture", 0, "192.168.50.10", domain.ClaimIPv4)
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
	q := DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: base}
	page, err := reader.ListDeviceActivity(context.Background(), q)
	if err != nil || len(page.Items) != 1 || page.Items[0].UserLabel != "staged" {
		t.Fatal(page, err)
	}
	q.AsOf = base.Add(time.Nanosecond)
	if page, err := reader.ListDeviceActivity(context.Background(), q); err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	var label string
	if err := s.conn.QueryRowContext(context.Background(), "SELECT user_label FROM devices").Scan(&label); err != nil || label != "Fixture device" {
		t.Fatal(label, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListDeviceActivity(context.Background(), q); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if page, err := readMixedActivity(t, db, base, q); err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
}

func TestMixedActivityLegacyMetadataAndMissingSchema(t *testing.T) {
	for _, mode := range []string{"oversized-stream", "oversized-attribution", "missing-schema"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			r := activityRecord(1, "device.fixture", 0, "192.168.50.10", domain.ClaimIPv4)
			storeLegacyDetailRecord(t, s, r)
			switch mode {
			case "oversized-stream":
				if _, err := db.Exec("UPDATE observations SET source_stream=?", strings.Repeat("x", 129)); err != nil {
					t.Fatal(err)
				}
			case "oversized-attribution":
				if _, err := db.Exec("UPDATE observations SET attribution=?", strings.Repeat("x", 257)); err != nil {
					t.Fatal(err)
				}
			case "missing-schema":
				if _, err := db.Exec("DROP TABLE evidence_batch_identity_routes"); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := readMixedActivity(t, db, r.Claims[0].Claim.ObservedAt, DeviceActivityQuery{ScopeID: "scope.fixture"}); err == nil || !reflect.DeepEqual(got, DeviceActivityPage{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestMixedActivityPreservesLegacyAggregateAndTieRules(t *testing.T) {
	legacy, _ := detailBatchFixture(t)
	_, db := detailBatchFixture(t)
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for n := 1; n <= 3; n++ {
		r := activityRecord(n, "device.fixture", 0, "203.0.113.9", domain.ClaimIPv4)
		// A supported noncanonical record can contain more than one address claim.
		// Preserve the legacy independently defined MAX aggregates and claim time.
		c := r.Claims[1]
		c.Claim.ID = fmt.Sprintf("claim.extra.%d", n)
		c.Claim.Kind = domain.ClaimIPv6
		c.Claim.Value = "2001:db8::1"
		c.Claim.ObservedAt = base.Add(time.Duration(3-n) * time.Minute)
		l := r.Links[1]
		l.ID = fmt.Sprintf("link.extra.%d", n)
		l.ClaimID = c.Claim.ID
		r.Claims = append(r.Claims, c)
		r.Links = append(r.Links, l)
		storeLegacyDetailRecord(t, legacy, r)
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	now := base.Add(time.Hour)
	legacy.now = func() time.Time { return now }
	for _, limit := range []int{1, 100} {
		q := DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: now, Limit: limit}
		want, werr := legacy.ListDeviceActivity(context.Background(), q)
		got, gerr := readMixedActivity(t, db, now, q)
		if werr != nil || gerr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, gerr, want, werr)
		}
	}
}

func TestMixedActivityUsesIndexedDeviceBatchSelection(t *testing.T) {
	_, db := detailBatchFixture(t)
	at := batchRecordFixture().Claims[0].Claim.ObservedAt.UnixNano()
	queries := []struct {
		sql  string
		args []any
	}{
		{activityCandidateDeviceSQL, []any{"", at, "scope.fixture", at, at, at, at, at, "scope.fixture", at, at, at}},
		{activityCandidateBatchSQL, []any{"device.fixture", "scope.fixture", at, at, at, true, int64(0)}},
	}
	for _, q := range queries {
		rows, err := db.Query("EXPLAIN QUERY PLAN "+q.sql, q.args...)
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
			t.Log(detail)
			found = found || strings.Contains(detail, "SEARCH r USING COVERING INDEX evidence_batch_identity_device")
			if strings.Contains(detail, "SCAN b") {
				t.Fatal("unbounded batch scan", detail)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !found {
			t.Fatal("device routing index not used")
		}
	}
}

func TestMixedHistoryProjectsOffsetTimestampsAsLegacyUTC(t *testing.T) {
	legacy, _ := detailBatchFixture(t)
	_, db := detailBatchFixture(t)
	r := activityRecord(1, "device.fixture", 0, "192.168.50.10", domain.ClaimIPv4)
	zone := time.FixedZone("fixture", -4*60*60)
	r.Observation.IngestedAt = r.Observation.IngestedAt.In(zone)
	for j := range r.Claims {
		r.Claims[j].Claim.ObservedAt = r.Claims[j].Claim.ObservedAt.In(zone)
		at := r.Claims[j].Claim.ValidUntil.In(zone)
		r.Claims[j].Claim.ValidUntil = &at
		linkAt := r.Links[j].ValidUntil.In(zone)
		r.Links[j].ValidUntil = &linkAt
	}
	storeLegacyDetailRecord(t, legacy, r)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	now := batchRecordFixture().Claims[0].Claim.ObservedAt.Add(time.Hour)
	legacy.now = func() time.Time { return now }
	aq := DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: now}
	wantActivity, werr := legacy.ListDeviceActivity(context.Background(), aq)
	gotActivity, gerr := readMixedActivity(t, db, now, aq)
	if werr != nil || gerr != nil || !reflect.DeepEqual(gotActivity, wantActivity) {
		t.Fatal(gotActivity, gerr, wantActivity, werr)
	}
	dq := DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: now}
	wantDetail, werr := legacy.GetDeviceEvidenceDetail(context.Background(), dq)
	gotDetail, gerr := readMixedDetail(t, db, now, dq)
	if werr != nil || gerr != nil || !reflect.DeepEqual(gotDetail, wantDetail) {
		t.Fatal(gotDetail, gerr, wantDetail, werr)
	}
	lq := DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: now}
	wantList, werr := legacy.ListDeviceEvidence(context.Background(), lq)
	gotList, gerr := readMixedDevices(t, db, now, lq)
	if werr != nil || gerr != nil || !reflect.DeepEqual(gotList, wantList) {
		t.Fatal(gotList, gerr, wantList, werr)
	}
	// Original wire evidence retains its offset; only query projections use UTC.
	raw, err := batchSQLRead(t, db, "scope.fixture", r.Observation.ID, now)
	if err != nil || raw.Observation.IngestedAt.Format(time.RFC3339Nano) != r.Observation.IngestedAt.Format(time.RFC3339Nano) || raw.Claims[0].Claim.ObservedAt.Format(time.RFC3339Nano) != r.Claims[0].Claim.ObservedAt.Format(time.RFC3339Nano) {
		t.Fatal("stored offset changed", raw, err)
	}
}
