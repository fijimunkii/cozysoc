package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestDeviceObservationSplitIsScopedAuditedAndUndoable(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	if err := s.CreateDevice(ctx, domain.Device{ID: "device.source", CreatedAt: base}); err != nil {
		t.Fatal(err)
	}
	first := activityRecord(1, "device.source", 0, "192.168.50.10", domain.ClaimIPv4)
	second := activityRecord(2, "device.source", time.Minute, "192.168.50.20", domain.ClaimIPv4)
	storeLegacyDetailRecord(t, s, first)
	if ok, err := batchSQLAppend(t, db, second); err != nil || !ok {
		t.Fatal(ok, err)
	}
	now := base.Add(2 * time.Minute)
	s.now = func() time.Time { return now }
	if target, changed, err := s.SplitDeviceObservation(ctx, "scope.other", "device.source", second.Observation.ID, ""); target != "" || changed || !errors.Is(err, ErrDeviceNotInScope) {
		t.Fatal("cross-scope split", target, changed, err)
	}
	target, changed, err := s.SplitDeviceObservation(ctx, "scope.fixture", "device.source", second.Observation.ID, "")
	if err != nil || !changed || target == "" || target == "device.source" {
		t.Fatal("split", target, changed, err)
	}
	again, changed, err := s.SplitDeviceObservation(ctx, "scope.fixture", "device.source", second.Observation.ID, "")
	if err != nil || changed || again != target {
		t.Fatal("idempotent split", again, changed, err)
	}
	items, err := s.ListDeviceSplits(ctx, "scope.fixture")
	if err != nil || len(items) != 1 || items[0].SourceDeviceID != "device.source" || items[0].TargetDeviceID != target || items[0].ObservationID != second.Observation.ID {
		t.Fatal("split listing", items, err)
	}
	if original, err := batchSQLRead(t, db, "scope.fixture", second.Observation.ID, now); err != nil || len(original.Links) != len(second.Links) || original.Links[0].DeviceID != "device.source" {
		t.Fatal("original evidence changed", original, err)
	}
	var links int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM device_identity_split_links WHERE scope_id=? AND observation_id=?`, "scope.fixture", second.Observation.ID).Scan(&links); err != nil || links != len(second.Links) {
		t.Fatal("split link mapping", links, err)
	}
	sourceDetail, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.source", AsOf: now})
	if err != nil || len(sourceDetail.Evidence) != len(first.Links) || !sourceDetail.Summary.LastSeen.Equal(base) {
		t.Fatal("split source detail", sourceDetail, err)
	}
	targetDetail, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: target, AsOf: now})
	if err != nil || len(targetDetail.Evidence) != len(second.Links) || !targetDetail.Summary.LastSeen.Equal(base.Add(time.Minute)) {
		t.Fatal("split target detail", targetDetail, err)
	}
	for _, item := range targetDetail.Evidence {
		if item.OriginalDeviceID != "device.source" || item.Authority != domain.LinkInferred {
			t.Fatal("split erased original association", item)
		}
	}
	page, err := readMixedDevices(t, db, now, DeviceEvidenceQuery{ScopeID: "scope.fixture", AsOf: now, Limit: 10})
	if err != nil || len(page.Devices) != 2 {
		t.Fatal("corrected device list", page, err)
	}
	seen := map[string]time.Time{}
	for _, item := range page.Devices {
		seen[item.Device.ID] = item.LastSeen
	}
	if !seen["device.source"].Equal(base) || !seen[target].Equal(base.Add(time.Minute)) {
		t.Fatal("corrected device summaries", page)
	}
	members, err := readMergedScopeDevices(t, db, now, DeviceQuery{ScopeID: "scope.fixture", AsOf: now, Limit: 10})
	if err != nil || len(members.Devices) != 2 {
		t.Fatal("corrected scope membership", members, err)
	}
	activity, err := readMixedActivity(t, db, now, DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: now})
	if err != nil || len(activity.Items) != 2 {
		t.Fatal("corrected activity", activity, err)
	}
	for _, item := range activity.Items {
		if item.Kind == DeviceActivityAddressChanged || item.ID == second.Observation.ID && item.DeviceID != target {
			t.Fatal("activity kept false address transition", item)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewMixedIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	candidates, candidateErr := view.FindRecentDevicesByClaim(ctx, "scope.fixture", domain.ClaimMAC, "02:00:00:00:00:01", base.Add(-time.Minute), now)
	_ = tx.Rollback()
	if candidateErr != nil || len(candidates) != 2 {
		t.Fatal("split MAC should remain ambiguous", candidates, candidateErr)
	}
	if changed, err := s.UndoDeviceSplitObservation(ctx, "scope.fixture", second.Observation.ID); err != nil || !changed {
		t.Fatal("undo split", changed, err)
	}
	if changed, err := s.UndoDeviceSplitObservation(ctx, "scope.fixture", second.Observation.ID); err != nil || changed {
		t.Fatal("idempotent undo", changed, err)
	}
	var audits int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE kind='device-identity'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("audit transitions", audits, err)
	}
}

func TestDeviceObservationSplitRollsBackWithoutAudit(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	r := activityRecord(1, "device.source", 0, "192.168.50.10", domain.ClaimIPv4)
	if err := s.CreateDevice(ctx, domain.Device{ID: "device.source", CreatedAt: r.Claims[0].Claim.ObservedAt}); err != nil {
		t.Fatal(err)
	}
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.now = func() time.Time { return r.Claims[0].Claim.ObservedAt.Add(time.Minute) }
	if _, err := s.conn.ExecContext(ctx, `CREATE TRIGGER reject_device_identity_audit
		BEFORE INSERT ON audit_events WHEN NEW.kind='device-identity'
		BEGIN SELECT RAISE(ABORT, 'fixture audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if target, changed, err := s.SplitDeviceObservation(ctx, "scope.fixture", "device.source", r.Observation.ID, ""); target != "" || changed || err == nil {
		t.Fatal("audit failure admitted split", target, changed, err)
	}
	var splits, devices int
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM device_identity_splits`).Scan(&splits); err != nil || splits != 0 {
		t.Fatal("split escaped rollback", splits, err)
	}
	if err := s.conn.QueryRowContext(ctx, `SELECT count(*) FROM devices`).Scan(&devices); err != nil || devices != 1 {
		t.Fatal("target escaped rollback", devices, err)
	}
}

func TestDeviceSplitRemainsProjectedAfterSourceDetailPageAdvances(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	if err := s.CreateDevice(ctx, domain.Device{ID: "device.source", CreatedAt: base}); err != nil {
		t.Fatal(err)
	}
	if ok, err := batchSQLAppend(t, db, activityRecord(1, "device.source", 0, "192.168.50.10", domain.ClaimIPv4)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	selected := activityRecord(2, "device.source", time.Minute, "192.168.50.20", domain.ClaimIPv4)
	if ok, err := batchSQLAppend(t, db, selected); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	target, changed, err := s.SplitDeviceObservation(ctx, "scope.fixture", "device.source", selected.Observation.ID, "")
	if err != nil || !changed {
		t.Fatal(target, changed, err)
	}
	for i := 3; i <= 110; i++ {
		r := activityRecord(i, "device.source", time.Duration(i)*time.Minute, "192.168.50.30", domain.ClaimIPv4)
		if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
			t.Fatal(i, ok, err)
		}
	}
	now := base.Add(111 * time.Minute)
	detail, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: target, AsOf: now})
	if err != nil || len(detail.Evidence) != 2 || !detail.Summary.FirstSeen.Equal(base.Add(time.Minute)) || !detail.Summary.LastSeen.Equal(base.Add(time.Minute)) {
		t.Fatal("older correction fell off source page", detail, err)
	}
	source, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.source", AsOf: now, Limit: 1})
	if err != nil || !source.Summary.FirstSeen.Equal(base) {
		t.Fatal("source first-seen drifted", source.Summary, err)
	}
}

func TestDeviceSplitOfFirstObservationMovesSourceFirstSeen(t *testing.T) {
	s, db := batchSQLFixture(t)
	ctx := context.Background()
	base := batchRecordFixture().Claims[0].Claim.ObservedAt
	if err := s.CreateDevice(ctx, domain.Device{ID: "device.source", CreatedAt: base}); err != nil {
		t.Fatal(err)
	}
	first := activityRecord(1, "device.source", 0, "192.168.50.10", domain.ClaimIPv4)
	second := activityRecord(2, "device.source", time.Minute, "192.168.50.20", domain.ClaimIPv4)
	if ok, err := batchSQLAppend(t, db, first); err != nil || !ok {
		t.Fatal(ok, err)
	}
	storeLegacyDetailRecord(t, s, second)
	now := base.Add(2 * time.Minute)
	s.now = func() time.Time { return now }
	target, changed, err := s.SplitDeviceObservation(ctx, "scope.fixture", "device.source", first.Observation.ID, "")
	if err != nil || !changed {
		t.Fatal(target, changed, err)
	}
	source, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.source", AsOf: now})
	if err != nil || !source.Summary.FirstSeen.Equal(base.Add(time.Minute)) || !source.Summary.LastSeen.Equal(base.Add(time.Minute)) {
		t.Fatal("source retained removed first-seen", source.Summary, err)
	}
	corrected, err := readMixedDetail(t, db, now, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: target, AsOf: now})
	if err != nil || !corrected.Summary.FirstSeen.Equal(base) {
		t.Fatal("target first-seen", corrected.Summary, err)
	}
	activity, err := readMixedActivity(t, db, now, DeviceActivityQuery{ScopeID: "scope.fixture", AsOf: now})
	if err != nil || len(activity.Items) != 2 {
		t.Fatal("split activity", activity, err)
	}
	for _, item := range activity.Items {
		if item.Kind != DeviceActivityFirstObserved {
			t.Fatal("corrected identity did not start at its first observation", item)
		}
	}
}
