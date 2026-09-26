package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestDeviceMergePreservesMixedEvidenceAndCanBeUndone(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for _, device := range []domain.Device{
		{ID: "device.target", UserLabel: "Kitchen", CreatedAt: base},
		{ID: "device.source", UserLabel: "Earlier guess", CreatedAt: base.Add(time.Minute)},
	} {
		if err := s.CreateDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
	}
	target := activityRecord(1, "device.target", 0, "192.168.50.10", domain.ClaimIPv4)
	source := activityRecord(2, "device.source", time.Minute, "192.168.50.20", domain.ClaimIPv4)
	storeLegacyDetailRecord(t, s, target)
	if ok, err := batchSQLAppend(t, db, source); err != nil || !ok {
		t.Fatal(ok, err)
	}
	now := base.Add(2 * time.Minute)
	s.now = func() time.Time { return now }
	query := DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: now, Limit: 10}
	before, err := readMixedDevices(t, db, now, query)
	if err != nil || len(before.Devices) != 2 {
		t.Fatal("before merge", before, err)
	}
	if changed, err := s.MergeDevices(ctx, "scope.other", "device.source", "device.target"); changed || !errors.Is(err, ErrDeviceNotInScope) {
		t.Fatal("cross-scope merge", changed, err)
	}
	if changed, err := s.MergeDevices(ctx, "scope.fixture", "device.source", "device.target"); err != nil || !changed {
		t.Fatal("merge", changed, err)
	}
	if changed, err := s.MergeDevices(ctx, "scope.fixture", "device.source", "device.target"); err != nil || changed {
		t.Fatal("idempotent merge", changed, err)
	}
	if changed, err := s.MergeDevices(ctx, "scope.fixture", "device.target", "device.source"); changed || !errors.Is(err, ErrDeviceMergeConflict) {
		t.Fatal("merge cycle", changed, err)
	}
	activeMerges, err := s.ListDeviceMerges(ctx, "scope.fixture")
	if err != nil || len(activeMerges) != 1 || activeMerges[0].SourceDeviceID != "device.source" || activeMerges[0].TargetDeviceID != "device.target" {
		t.Fatal("active merge listing", activeMerges, err)
	}
	for _, id := range []string{"device.source", "device.target"} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		deleted, deleteErr := DeleteEvidenceBatchDevice(ctx, tx, id)
		_ = tx.Rollback()
		if deleted || !errors.Is(deleteErr, ErrDeviceMergeConflict) {
			t.Fatal("corrected device deletion was not rejected", id, deleted, deleteErr)
		}
	}
	after, err := readMixedDevices(t, db, now, query)
	if err != nil || len(after.Devices) != 1 || after.Devices[0].Device.ID != "device.target" || after.Devices[0].Device.UserLabel != "Kitchen" || !after.Devices[0].LastSeen.Equal(base.Add(time.Minute)) {
		t.Fatal("merged list", after, err)
	}
	detail, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.target", AsOf: now})
	if err != nil || len(detail.Evidence) != 4 || detail.Summary.Device.ID != "device.target" {
		t.Fatal("merged detail", detail, err)
	}
	fromSource := 0
	for _, item := range detail.Evidence {
		if item.OriginalDeviceID == "device.source" {
			fromSource++
		}
	}
	if fromSource != 2 {
		t.Fatal("original association missing", detail.Evidence)
	}
	scoped, err := readMergedScopeDevices(t, db, now, DeviceQuery{ScopeID: "scope.fixture", AsOf: now, Limit: 10})
	if err != nil || len(scoped.Devices) != 1 || scoped.Devices[0].ID != "device.target" {
		t.Fatal("merged current membership", scoped, err)
	}
	historical, err := readMergedScopeDevices(t, db, now, DeviceQuery{ScopeID: "scope.fixture", AsOf: base.Add(12 * time.Minute), Limit: 10})
	if err != nil || len(historical.Devices) != 0 {
		t.Fatal("expired association stayed current", historical, err)
	}
	if changed, err := s.SetDeviceLabel(ctx, "scope.fixture", "device.source", "Hidden label"); changed || !errors.Is(err, ErrDeviceNotInScope) {
		t.Fatal("merged source accepted a label", changed, err)
	}
	if _, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.source", AsOf: now}); !errors.Is(err, ErrDeviceEvidenceNotFound) {
		t.Fatal("source still projected", err)
	}
	activity, err := readMixedActivity(t, db, now, DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: now})
	if err != nil || len(activity.Items) != 2 || activity.Items[0].DeviceID != "device.target" || activity.Items[0].Kind != DeviceActivityAddressChanged || activity.Items[0].PreviousAddress != "192.168.50.10" {
		t.Fatal("merged activity", activity, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := view.FindRecentDevicesByClaim(ctx, "scope.fixture", domain.ClaimMAC, "02:00:00:00:00:01", base.Add(-time.Minute), now)
	_ = tx.Rollback()
	if err != nil || len(candidates) != 1 || candidates[0].ID != "device.target" {
		t.Fatal("future reconciliation candidate", candidates, err)
	}
	if changed, err := s.UnmergeDevices(ctx, "scope.fixture", "device.source"); err != nil || !changed {
		t.Fatal("unmerge", changed, err)
	}
	if changed, err := s.UnmergeDevices(ctx, "scope.fixture", "device.source"); err != nil || changed {
		t.Fatal("idempotent unmerge", changed, err)
	}
	activeMerges, err = s.ListDeviceMerges(ctx, "scope.fixture")
	if err != nil || len(activeMerges) != 0 {
		t.Fatal("unmerge left active projection", activeMerges, err)
	}
	restored, err := readMixedDevices(t, db, now, query)
	if err != nil || len(restored.Devices) != 2 {
		t.Fatal("restored devices", restored, err)
	}
	var audits int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE kind='device-identity'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("audit transitions", audits, err)
	}
	if original, err := batchSQLRead(t, db, "scope.fixture", source.Observation.ID, now); err != nil || len(original.Links) != len(source.Links) || original.Links[0].DeviceID != "device.source" {
		t.Fatal("original batch evidence changed", original, err)
	}
}

func readMergedScopeDevices(t *testing.T, db *sql.DB, now time.Time, q DeviceQuery) (DevicePage, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	view, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	return view.ListDevicesForScope(context.Background(), q)
}

func TestDeviceMergeRollsBackWithoutAudit(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	for _, id := range []string{"device.source", "device.target"} {
		if err := s.CreateDevice(ctx, domain.Device{ID: id, CreatedAt: base}); err != nil {
			t.Fatal(err)
		}
	}
	storeLegacyDetailRecord(t, s, deviceListRecord(1, "device.target", 0))
	if ok, err := batchSQLAppend(t, db, deviceListRecord(2, "device.source", 0)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.now = func() time.Time { return base.Add(time.Minute) }
	if _, err := s.conn.ExecContext(ctx, `CREATE TRIGGER reject_device_identity_audit
		BEFORE INSERT ON audit_events WHEN NEW.kind='device-identity'
		BEGIN SELECT RAISE(ABORT, 'fixture audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.MergeDevices(ctx, "scope.fixture", "device.source", "device.target"); changed || err == nil {
		t.Fatal("audit failure admitted merge", changed, err)
	}
	var count int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM device_identity_merges`).Scan(&count); err != nil || count != 0 {
		t.Fatal("merge escaped rollback", count, err)
	}
}

func TestDeviceMergePaginationUsesCorrectedDeviceIDs(t *testing.T) {
	for _, tc := range []struct {
		name, source, target     string
		wantPageOne, wantPageTwo string
	}{
		{"source_before_target", "device.a", "device.z", "device.m", "device.z"},
		{"source_after_target", "device.z", "device.a", "device.a", "device.m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := batchSQLFixture(t)
			ctx := context.Background()
			base := batchRecordFixture().Claims[0].Claim.ObservedAt
			for _, id := range []string{"device.a", "device.m", "device.z"} {
				if err := s.CreateDevice(ctx, domain.Device{ID: id, CreatedAt: base}); err != nil {
					t.Fatal(err)
				}
			}
			for i, id := range []string{"device.a", "device.m", "device.z"} {
				r := deviceListRecord(i+1, id, 0)
				if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			now := base.Add(time.Minute)
			s.now = func() time.Time { return now }
			if changed, err := s.MergeDevices(ctx, "scope.fixture", tc.source, tc.target); err != nil || !changed {
				t.Fatal(changed, err)
			}
			first, err := readMixedDevices(t, db, now, DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: now, Limit: 1})
			if err != nil || len(first.Devices) != 1 || first.Devices[0].Device.ID != tc.wantPageOne || first.NextID != tc.wantPageOne {
				t.Fatal("first corrected page", first, err)
			}
			second, err := readMixedDevices(t, db, now, DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: now, AfterID: first.NextID, Limit: 1})
			if err != nil || len(second.Devices) != 1 || second.Devices[0].Device.ID != tc.wantPageTwo || second.NextID != "" {
				t.Fatal("second corrected page", second, err)
			}
		})
	}
}
