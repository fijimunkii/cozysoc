package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestLatestCoverageSampleUsesEvidenceTimeNotInsertionOrder(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{
		ID: "scope.home", Kind: "lan", EnrolledAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{"schema_version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{
		ID: "sensor.dw.test", ScopeID: "scope.home", Kind: "desktop-neighbor-cache", Ownership: "builtin",
		RegisteredAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{"schema_version":1}`),
	}); err != nil {
		t.Fatal(err)
	}

	fresh := domain.CoverageSample{
		ID: "coverage.fresh", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: "device-watch",
		Status: "partial", StartedAt: now.Add(-time.Minute), EndedAt: now.Add(-time.Minute), SchemaVersion: 1,
		Evidence: json.RawMessage(`{"schema_version":1}`), Retention: domain.RetentionShort,
	}
	if err := store.InsertCoverageSample(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	oldReplay := fresh
	oldReplay.ID = "coverage.replayed-old"
	oldReplay.StartedAt = now.Add(-time.Hour)
	oldReplay.EndedAt = now.Add(-time.Hour)
	if err := store.InsertCoverageSample(ctx, oldReplay); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.LatestCoverageSample(ctx, "scope.home", "device-watch")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.ID != fresh.ID || !got.EndedAt.Equal(fresh.EndedAt) {
		t.Fatalf("latest coverage = %+v ok=%v", got, ok)
	}
}

func TestLatestCoverageSampleHonorsLogicalExpiry(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{
		ID: "scope.home", Kind: "lan", EnrolledAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{"schema_version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{
		ID: "sensor.dw.test", ScopeID: "scope.home", Kind: "desktop-neighbor-cache", Ownership: "builtin",
		RegisteredAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{"schema_version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertCoverageSample(ctx, domain.CoverageSample{
		ID: "coverage.short", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: "device-watch",
		Status: "partial", StartedAt: now, EndedAt: now, SchemaVersion: 1,
		Evidence: json.RawMessage(`{"schema_version":1}`), Retention: domain.RetentionShort,
	}); err != nil {
		t.Fatal(err)
	}

	store.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	if got, ok, err := store.LatestCoverageSample(ctx, "scope.home", "device-watch"); err != nil || ok {
		t.Fatalf("expired coverage = %+v ok=%v err=%v", got, ok, err)
	}
}
