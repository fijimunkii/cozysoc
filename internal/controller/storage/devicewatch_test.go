package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestDeviceWatchIdentityHelpersPreserveTemporalEvidence(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0).UTC()
	seedScopeAndSensor(t, store, "scope.home", "sensor.dw.fixture", t0)

	for index, at := range []time.Time{t0, t0.Add(time.Hour)} {
		observationID := "obs.dw.fixture." + string(rune('a'+index))
		observation := observationFixture(observationID, "scope.home", "sensor.dw.fixture", at)
		observation.SourceKey = observationID
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
			t.Fatalf("insert observation %d = %v, %v", index, inserted, err)
		}
		validUntil := at.Add(10 * time.Minute)
		confidence := 0.9
		claimID, err := store.EnsureIdentityClaim(ctx, domain.IdentityClaim{
			ID:                  "claim.dw.fixture." + string(rune('a'+index)),
			ScopeID:             "scope.home",
			Kind:                domain.ClaimMAC,
			Value:               "02:00:00:00:00:01",
			ObservedAt:          at,
			ValidUntil:          &validUntil,
			Confidence:          &confidence,
			SourceSensorID:      "sensor.dw.fixture",
			SourceObservationID: observationID,
			Retention:           domain.RetentionStandard,
		})
		if err != nil {
			t.Fatal(err)
		}
		device := domain.Device{ID: "device.fixture", CreatedAt: t0}
		if err := store.EnsureDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureDeviceClaimLink(ctx, domain.DeviceClaimLink{
			ID:                    "link.dw.fixture." + string(rune('a'+index)),
			DeviceID:              device.ID,
			ClaimID:               claimID,
			ValidFrom:             at,
			ValidUntil:            &validUntil,
			Confidence:            &confidence,
			Authority:             domain.LinkInferred,
			Reason:                "fixture",
			EvidenceObservationID: observationID,
			CreatedAt:             at,
		}); err != nil {
			t.Fatal(err)
		}
	}

	devices, err := store.FindRecentDevicesByClaim(ctx, "scope.home", domain.ClaimMAC, "02:00:00:00:00:01", t0.Add(-time.Hour), t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != "device.fixture" {
		t.Fatalf("unexpected continuity candidates: %+v", devices)
	}
	page, err := store.ListDeviceEvidence(ctx, DeviceEvidenceQuery{ScopeID: "scope.home", AsOf: t0.Add(2 * time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Devices) != 1 {
		t.Fatalf("device evidence count = %d", len(page.Devices))
	}
	if !page.Devices[0].FirstSeen.Equal(t0) || !page.Devices[0].LastSeen.Equal(t0.Add(time.Hour)) {
		t.Fatalf("unexpected first/last seen: %+v", page.Devices[0])
	}
}

func TestEnsureSensorIsIdempotentButRejectsIdentityChange(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{
		ID: "scope.home", Kind: "lan", EnrolledAt: now, Metadata: json.RawMessage(`{"fixture":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	sensor := domain.Sensor{
		ID: "sensor.dw.fixture", ScopeID: "scope.home", Kind: "desktop-neighbor-cache", Ownership: "builtin",
		RegisteredAt: now, Metadata: json.RawMessage(`{"interface":"en0"}`),
	}
	if err := store.EnsureSensor(ctx, sensor); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSensor(ctx, sensor); err != nil {
		t.Fatalf("idempotent ensure failed: %v", err)
	}
	changed := sensor
	changed.Kind = "different-kind"
	if err := store.EnsureSensor(ctx, changed); err == nil {
		t.Fatal("sensor identity change was accepted")
	}
}

func TestGetNetworkScopeNotFoundIsTyped(t *testing.T) {
	store := openTestStore(t)
	_, err := store.GetNetworkScope(context.Background(), "scope.missing")
	if !errors.Is(err, ErrNetworkScopeNotFound) {
		t.Fatalf("missing scope error = %v", err)
	}
}
