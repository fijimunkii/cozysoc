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

func legacySnapshotFixture(t *testing.T) (*Store, *sql.DB, time.Time) {
	t.Helper()
	s := openTestStore(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	seedScopeAndSensor(t, s, "scope.home", "sensor.home", now)
	seedScopeAndSensor(t, s, "scope.other", "sensor.other", now)
	uri, err := sqliteFileURI(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", uri+"?mode=rw&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)&_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return s, db, now
}

func seedSnapshotIdentity(t *testing.T, s *Store, id, scope, sensor string, observed, expires time.Time, retired *time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.EnsureDevice(ctx, domain.Device{ID: id, CreatedAt: observed, RetiredAt: retired}); err != nil {
		t.Fatal(err)
	}
	valid := observed.Add(time.Minute)
	claim := domain.IdentityClaim{ID: "claim." + id, ScopeID: scope, Kind: domain.ClaimMAC, Value: "02:aa:bb:cc:dd:01", ObservedAt: observed, ValidUntil: &valid, SourceSensorID: sensor, Retention: domain.RetentionStandard}
	if err := s.InsertIdentityClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(ctx, "UPDATE identity_claims SET expires_at_ns=? WHERE id=?", expires.UnixNano(), claim.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{ID: "link." + id, DeviceID: id, ClaimID: claim.ID, ValidFrom: observed, ValidUntil: &valid, Authority: domain.LinkInferred, Reason: "fixture", CreatedAt: observed}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyIdentitySnapshotPreservesQuerySemantics(t *testing.T) {
	s, db, now := legacySnapshotFixture(t)
	since, until := now.Add(-time.Hour), now
	before := until.Add(-time.Nanosecond)
	for _, tc := range []struct {
		id, scope, sensor string
		at, expiry        time.Time
		retired           *time.Time
	}{
		{"device.a", "scope.home", "sensor.home", since, now.Add(time.Hour), nil},
		{"device.b", "scope.home", "sensor.home", until, now.Add(time.Hour), &until},
		{"device.c", "scope.home", "sensor.home", since.Add(time.Minute), now.Add(time.Hour), nil},
		{"device.d", "scope.home", "sensor.home", since.Add(time.Minute), now.Add(time.Hour), nil},
		{"device.0expired", "scope.home", "sensor.home", since, now, nil},
		{"device.0old", "scope.home", "sensor.home", since.Add(-time.Nanosecond), now.Add(time.Hour), nil},
		{"device.0future", "scope.home", "sensor.home", until.Add(time.Nanosecond), now.Add(time.Hour), nil},
		{"device.0retired", "scope.home", "sensor.home", since, now.Add(time.Hour), &before},
		{"device.0other", "scope.other", "sensor.other", since, now.Add(time.Hour), nil},
	} {
		seedSnapshotIdentity(t, s, tc.id, tc.scope, tc.sensor, tc.at, tc.expiry, tc.retired)
	}
	if err := s.CreateDeviceClaimLink(context.Background(), domain.DeviceClaimLink{ID: "link.duplicate", DeviceID: "device.a", ClaimID: "claim.device.a", ValidFrom: since, Authority: domain.LinkInferred, Reason: "fixture", CreatedAt: since}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := NewLegacyIdentitySnapshot(tx, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.FindRecentDevicesByClaim(context.Background(), "scope.home", domain.ClaimMAC, "02-AA-BB-CC-DD-01", since, until)
	if err != nil {
		t.Fatal(err)
	}
	want, err := s.FindRecentDevicesByClaim(context.Background(), "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", since, until)
	if err != nil || !reflect.DeepEqual(got, want) || len(got) != 3 || got[0].ID != "device.a" || got[1].ID != "device.b" || got[2].ID != "device.c" {
		t.Fatal("changed ordering, boundaries or ambiguity cap", got, want, err)
	}
	// The validity interval is deliberately past. Recent identity continuity uses
	// observation time and retention expiry, not current-presence validity.
	later, err := NewLegacyIdentitySnapshot(tx, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err = later.FindRecentDevicesByClaim(context.Background(), "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", since, until)
	if err != nil || len(got) != 0 {
		t.Fatal("retention clock or exact expiry ignored", got, err)
	}
}

func TestLegacyIdentitySnapshotSeesOwnWritesAndLeavesTransactionOwned(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			s, db, now := legacySnapshotFixture(t)
			seedSnapshotIdentity(t, s, "device.original", "scope.home", "sensor.home", now, now.Add(time.Hour), nil)
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			reader, err := NewLegacyIdentitySnapshot(tx, now)
			if err != nil {
				t.Fatal(err)
			}
			query := func() ([]domain.Device, error) {
				return reader.FindRecentDevicesByClaim(ctx, "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", now, now)
			}
			if got, err := query(); err != nil || len(got) != 1 {
				t.Fatal(got, err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO devices(id,created_at_ns) VALUES('device.staged',?)", now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO device_claim_links(id,device_id,claim_id,valid_from_ns,authority,reason,created_at_ns) VALUES('link.staged','device.staged','claim.device.original',?,'inferred','fixture',?)`, now.UnixNano(), now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			got, err := query()
			if err != nil || len(got) != 2 || got[1].ID != "device.staged" {
				t.Fatal("snapshot missed staged ambiguity", got, err)
			}
			outside, err := s.FindRecentDevicesByClaim(ctx, "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", now, now)
			if err != nil || len(outside) != 1 {
				t.Fatal("uncommitted writes escaped transaction", outside, err)
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := query(); !errors.Is(err, sql.ErrTxDone) || got != nil {
				t.Fatal("reader escaped completed transaction", got, err)
			}
			outside, err = s.FindRecentDevicesByClaim(ctx, "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", now, now)
			want := 1
			if commit {
				want = 2
			}
			if err != nil || len(outside) != want {
				t.Fatal("transaction outcome", outside, err)
			}
		})
	}
}

func TestLegacyIdentitySnapshotRejectsInvalidQueries(t *testing.T) {
	_, db, now := legacySnapshotFixture(t)
	if _, err := NewLegacyIdentitySnapshot(nil, now); err == nil {
		t.Fatal("nil transaction accepted")
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, invalid := range []time.Time{{}, time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := NewLegacyIdentitySnapshot(tx, invalid); err == nil {
			t.Fatal("invalid clock accepted")
		}
	}
	reader, _ := NewLegacyIdentitySnapshot(tx, now)
	for _, tc := range []struct {
		scope, value string
		since, until time.Time
	}{
		{"", "02:aa:bb:cc:dd:01", now, now},
		{"scope.home", "invalid", now, now},
		{"scope.home", "02:aa:bb:cc:dd:01", time.Time{}, now},
		{"scope.home", "02:aa:bb:cc:dd:01", now, now.Add(-time.Second)},
		{"scope.home", "02:aa:bb:cc:dd:01", now.Add(-MaxQueryWindow - time.Nanosecond), now},
	} {
		if got, err := reader.FindRecentDevicesByClaim(context.Background(), tc.scope, domain.ClaimMAC, tc.value, tc.since, tc.until); err == nil || got != nil {
			t.Fatal("invalid query accepted", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := reader.FindRecentDevicesByClaim(ctx, "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", now, now); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("cancellation ignored", got, err)
	}
	var absent *LegacyIdentitySnapshot
	if _, err := absent.FindRecentDevicesByClaim(context.Background(), "scope.home", domain.ClaimMAC, "02:aa:bb:cc:dd:01", now, now); err == nil {
		t.Fatal("nil snapshot accepted")
	}
}
