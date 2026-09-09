package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestOpenCreatesPrivateMigratedRollbackStore(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %o, want 600", got)
	}
	version, err := store.SQLiteVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if compareVersion(version, minimumSQLiteVersion) < 0 {
		t.Fatalf("SQLite version %s is below %s", version, minimumSQLiteVersion)
	}
	mode, err := store.JournalMode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "delete" {
		t.Fatalf("journal mode = %q, want delete", mode)
	}
	schema, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if schema != schemaVersion {
		t.Fatalf("schema version = %d, want %d", schema, schemaVersion)
	}
	bytes, err := store.DatabaseBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytes <= 0 || bytes > testLimits().MaxBytes {
		t.Fatalf("database bytes = %d, limit = %d", bytes, testLimits().MaxBytes)
	}
}

func TestObservationReplayIsDeduplicatedBySensorStreamAndSourceKey(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	seedScopeAndSensor(t, store, "scope.home", "sensor.desktop", now)

	first := observationFixture("obs.1", "scope.home", "sensor.desktop", now)
	inserted, err := store.InsertObservation(ctx, first)
	if err != nil || !inserted {
		t.Fatalf("first insert = %v, %v", inserted, err)
	}
	duplicate := first
	duplicate.ID = "obs.2"
	duplicate.IngestedAt = now.Add(time.Second)
	inserted, err = store.InsertObservation(ctx, duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate source event was inserted")
	}
	count, err := store.ObservationCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("observation count = %d, want 1", count)
	}
}

func TestTemporalIdentityKeepsScopeAndAddressReuseSeparate(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0).UTC()
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", t0)
	seedScopeAndSensor(t, store, "scope.lab", "sensor.lab", t0)

	for _, item := range []struct {
		observationID string
		scopeID       string
		sensorID      string
		claimID       string
		at            time.Time
	}{
		{"obs.home.1", "scope.home", "sensor.home", "claim.home.1", t0},
		{"obs.home.2", "scope.home", "sensor.home", "claim.home.2", t0.Add(2 * time.Hour)},
		{"obs.lab.1", "scope.lab", "sensor.lab", "claim.lab.1", t0.Add(time.Hour)},
	} {
		observation := observationFixture(item.observationID, item.scopeID, item.sensorID, item.at)
		observation.SourceKey = item.observationID
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
			t.Fatalf("insert observation %s = %v, %v", item.observationID, inserted, err)
		}
		claim := domain.IdentityClaim{
			ID:                  item.claimID,
			ScopeID:             item.scopeID,
			Kind:                domain.ClaimIPv4,
			Value:               "192.168.1.20",
			ObservedAt:          item.at,
			SourceSensorID:      item.sensorID,
			SourceObservationID: item.observationID,
			Retention:           domain.RetentionStandard,
		}
		if err := store.InsertIdentityClaim(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}

	for _, device := range []domain.Device{
		{ID: "device.old", UserLabel: "Old occupant", CreatedAt: t0},
		{ID: "device.new", UserLabel: "New occupant", CreatedAt: t0.Add(2 * time.Hour)},
	} {
		if err := store.CreateDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
	}
	boundary := t0.Add(time.Hour)
	if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
		ID: "link.old", DeviceID: "device.old", ClaimID: "claim.home.1", ValidFrom: t0,
		ValidUntil: &boundary, Authority: domain.LinkInferred, Reason: "first DHCP occupant", CreatedAt: t0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
		ID: "link.new", DeviceID: "device.new", ClaimID: "claim.home.2", ValidFrom: t0.Add(2 * time.Hour),
		Authority: domain.LinkInferred, Reason: "later DHCP occupant", CreatedAt: t0.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	home, err := store.IdentityClaimCount(ctx, "scope.home", domain.ClaimIPv4, "192.168.1.20")
	if err != nil {
		t.Fatal(err)
	}
	lab, err := store.IdentityClaimCount(ctx, "scope.lab", domain.ClaimIPv4, "192.168.1.20")
	if err != nil {
		t.Fatal(err)
	}
	if home != 2 || lab != 1 {
		t.Fatalf("scope-aware claim counts home=%d lab=%d", home, lab)
	}
}

func TestCheckpointUpsertSupportsRestartReplay(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	seedScopeAndSensor(t, store, "scope.home", "sensor.desktop", now)

	for index, cursor := range []string{"cursor-1", "cursor-2"} {
		if err := store.SaveCheckpoint(ctx, domain.IngestionCheckpoint{
			SensorID: "sensor.desktop", StreamID: "neighbor-cache", Cursor: cursor, UpdatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, ok, err := store.LoadCheckpoint(ctx, "sensor.desktop", "neighbor-cache")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || checkpoint.Cursor != "cursor-2" {
		t.Fatalf("unexpected checkpoint: %+v, ok=%v", checkpoint, ok)
	}
}

func TestPruneExpiredRecordsVisibleStorageEvent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return base }
	seedScopeAndSensor(t, store, "scope.home", "sensor.desktop", base)

	observation := observationFixture("obs.expiring", "scope.home", "sensor.desktop", base)
	observation.Retention = domain.RetentionEphemeral
	if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
		t.Fatalf("insert expiring observation = %v, %v", inserted, err)
	}
	pruneAt := base.Add(2 * time.Hour)
	store.now = func() time.Time { return pruneAt }
	counts, err := store.PruneExpired(ctx, pruneAt, 100)
	if err != nil {
		t.Fatal(err)
	}
	if counts["observations"] != 1 {
		t.Fatalf("unexpected prune counts: %+v", counts)
	}
	remaining, err := store.ObservationCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("observation count after prune = %d", remaining)
	}
	events, err := store.StorageEventCount(ctx, "retention-expired")
	if err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("retention storage event count = %d, want 1", events)
	}
}

func TestFutureStorageSchemaIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir, testLimits())
	if store != nil {
		_ = store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("future storage schema accepted: %v", err)
	}
}

func TestDatabasePathRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, Filename)); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir, testLimits())
	if store != nil {
		_ = store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("symlink database path accepted: %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir(), testLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testLimits() Limits {
	return Limits{
		MaxBytes: 8 << 20,
		Retention: map[domain.RetentionClass]time.Duration{
			domain.RetentionEphemeral: time.Hour,
			domain.RetentionShort:     24 * time.Hour,
			domain.RetentionStandard:  7 * 24 * time.Hour,
			domain.RetentionAudit:     30 * 24 * time.Hour,
		},
	}
}

func seedScopeAndSensor(t *testing.T, store *Store, scopeID, sensorID string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{
		ID: scopeID, Kind: "lan", EnrolledAt: now, Metadata: json.RawMessage(`{"fixture":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{
		ID: sensorID, ScopeID: scopeID, Kind: "desktop-discovery", Ownership: "builtin", RegisteredAt: now,
		Metadata: json.RawMessage(`{"fixture":true}`),
	}); err != nil {
		t.Fatal(err)
	}
}

func observationFixture(id, scopeID, sensorID string, now time.Time) domain.Observation {
	source := now.Add(-time.Minute)
	return domain.Observation{
		ID:            id,
		ScopeID:       scopeID,
		SensorID:      sensorID,
		Kind:          "neighbor-seen",
		SourceStream:  "neighbor-cache",
		SourceKey:     id,
		SourceEventID: id,
		SourceTime:    &source,
		IngestedAt:    now,
		SchemaVersion: 1,
		Attribution:   "desktop-neighbor-cache",
		Payload:       json.RawMessage(`{"address":"192.168.1.20"}`),
		Retention:     domain.RetentionStandard,
	}
}
