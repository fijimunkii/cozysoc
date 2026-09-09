package devicewatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestReconcilerUsesRecentMACContinuityWithoutUsingIPAsIdentity(t *testing.T) {
	store, scopeID, sensorID := newIdentityFixtureStore(t)
	ctx := context.Background()
	reconciler, err := NewReconciler(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	first := neighborObservationFixture(t, scopeID, sensorID, "obs.dw.first", "192.168.1.20", "02:00:00:00:00:01", now)
	if inserted, err := store.InsertObservation(ctx, first); err != nil || !inserted {
		t.Fatalf("insert first observation = %v, %v", inserted, err)
	}
	firstResult, err := reconciler.ReconcileObservation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !firstResult.Created || firstResult.DeviceID == "" || firstResult.Ambiguous {
		t.Fatalf("unexpected first reconciliation: %+v", firstResult)
	}

	second := neighborObservationFixture(t, scopeID, sensorID, "obs.dw.second", "192.168.1.99", "02:00:00:00:00:01", now.Add(time.Hour))
	if inserted, err := store.InsertObservation(ctx, second); err != nil || !inserted {
		t.Fatalf("insert second observation = %v, %v", inserted, err)
	}
	secondResult, err := reconciler.ReconcileObservation(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.Created || secondResult.DeviceID != firstResult.DeviceID || secondResult.Ambiguous {
		t.Fatalf("same recent MAC did not preserve temporal device continuity: first=%+v second=%+v", firstResult, secondResult)
	}

	third := neighborObservationFixture(t, scopeID, sensorID, "obs.dw.third", "192.168.1.99", "02:00:00:00:00:02", now.Add(2*time.Hour))
	if inserted, err := store.InsertObservation(ctx, third); err != nil || !inserted {
		t.Fatalf("insert third observation = %v, %v", inserted, err)
	}
	thirdResult, err := reconciler.ReconcileObservation(ctx, third)
	if err != nil {
		t.Fatal(err)
	}
	if !thirdResult.Created || thirdResult.DeviceID == firstResult.DeviceID {
		t.Fatalf("same IP with different MAC was incorrectly merged: first=%+v third=%+v", firstResult, thirdResult)
	}
}

func TestReconcilerPreservesAmbiguousMACContinuity(t *testing.T) {
	store, scopeID, sensorID := newIdentityFixtureStore(t)
	ctx := context.Background()
	reconciler, err := NewReconciler(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	mac := "02:00:00:00:00:09"

	for index, deviceID := range []string{"device.one", "device.two"} {
		at := now.Add(time.Duration(index) * time.Minute)
		observationID := []string{"obs.dw.seed.one", "obs.dw.seed.two"}[index]
		observation := neighborObservationFixture(t, scopeID, sensorID, observationID, "192.168.1.50", mac, at)
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
			t.Fatalf("insert seed observation = %v, %v", inserted, err)
		}
		confidence := 0.75
		validUntil := at.Add(10 * time.Minute)
		claimID, err := store.EnsureIdentityClaim(ctx, domain.IdentityClaim{
			ID: "claim.seed." + []string{"one", "two"}[index], ScopeID: scopeID, Kind: domain.ClaimMAC, Value: mac,
			ObservedAt: at, ValidUntil: &validUntil, Confidence: &confidence, SourceSensorID: sensorID,
			SourceObservationID: observationID, Retention: domain.RetentionStandard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureDevice(ctx, domain.Device{ID: deviceID, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureDeviceClaimLink(ctx, domain.DeviceClaimLink{
			ID: "link.seed." + []string{"one", "two"}[index], DeviceID: deviceID, ClaimID: claimID,
			ValidFrom: at, ValidUntil: &validUntil, Confidence: &confidence, Authority: domain.LinkInferred,
			Reason: "fixture", EvidenceObservationID: observationID, CreatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}

	current := neighborObservationFixture(t, scopeID, sensorID, "obs.dw.ambiguous", "192.168.1.51", mac, now.Add(2*time.Minute))
	if inserted, err := store.InsertObservation(ctx, current); err != nil || !inserted {
		t.Fatalf("insert current observation = %v, %v", inserted, err)
	}
	result, err := reconciler.ReconcileObservation(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ambiguous || result.DeviceID != "" || result.Created || result.Claims != 2 {
		t.Fatalf("ambiguous MAC continuity was collapsed: %+v", result)
	}
}

func newIdentityFixtureStore(t *testing.T) (*storage.Store, string, string) {
	t.Helper()
	store, err := storage.Open(t.TempDir(), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	scopeID := "scope.home"
	sensorID := "sensor.dw.fixture"
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{
		ID: scopeID, Kind: "lan", EnrolledAt: now, Metadata: json.RawMessage(`{"fixture":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{
		ID: sensorID, ScopeID: scopeID, Kind: "desktop-neighbor-cache", Ownership: "builtin", RegisteredAt: now,
		Metadata: json.RawMessage(`{"fixture":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	return store, scopeID, sensorID
}

func neighborObservationFixture(t *testing.T, scopeID, sensorID, id, address, mac string, at time.Time) domain.Observation {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schema_version":   1,
		"address":          address,
		"hardware_address": mac,
		"interface":        "en0",
		"family": func() string {
			if len(address) > 0 && address[0] == '1' && len(address) > 3 && address[:3] == "192" {
				return "ipv4"
			}
			return "ipv6"
		}(),
		"method": "arp-cache",
		"state":  "reachable",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := at.UTC()
	return domain.Observation{
		ID: id, ScopeID: scopeID, SensorID: sensorID, Kind: "device-neighbor-seen",
		SourceStream: "device-watch-neighbors", SourceKey: id, SourceEventID: id, SourceTime: &source,
		IngestedAt: at.UTC(), SchemaVersion: 1, Attribution: "device-watch:arp-cache", Payload: payload,
		Retention: domain.RetentionStandard,
	}
}
