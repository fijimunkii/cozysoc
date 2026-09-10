package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestGetDeviceEvidenceDetailIsScopeBoundedOrderedAndHistorical(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return base.Add(30 * time.Minute) }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)
	seedScopeAndSensor(t, store, "scope.lab", "sensor.lab", base)

	device := domain.Device{ID: "device.one", UserLabel: "Kitchen speaker", CreatedAt: base}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	seedDetailEvidence(t, store, "scope.home", "sensor.home", device.ID, "obs.old", "claim.old", "link.old", domain.ClaimMAC, "02:00:00:00:00:01", base, base.Add(10*time.Minute))
	seedDetailEvidence(t, store, "scope.home", "sensor.home", device.ID, "obs.new", "claim.new", "link.new", domain.ClaimIPv4, "192.168.1.20", base.Add(25*time.Minute), base.Add(35*time.Minute))
	seedDetailEvidence(t, store, "scope.lab", "sensor.lab", device.ID, "obs.lab", "claim.lab", "link.lab", domain.ClaimIPv4, "10.0.0.20", base.Add(28*time.Minute), base.Add(38*time.Minute))

	detail, err := store.GetDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: "scope.home", DeviceID: device.ID, AsOf: base.Add(30 * time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.Device.ID != device.ID || detail.Summary.Device.UserLabel != "Kitchen speaker" || !detail.Summary.FirstSeen.Equal(base) || !detail.Summary.LastSeen.Equal(base.Add(25*time.Minute)) {
		t.Fatalf("unexpected summary: %+v", detail.Summary)
	}
	if detail.Truncated || len(detail.Evidence) != 2 || detail.Evidence[0].Value != "192.168.1.20" || detail.Evidence[1].Value != "02:00:00:00:00:01" {
		t.Fatalf("unexpected evidence ordering/scope: %+v", detail)
	}
	if detail.Evidence[0].Observation == nil || detail.Evidence[0].Observation.SensorID != "sensor.home" {
		t.Fatalf("missing bounded source provenance: %+v", detail.Evidence[0])
	}

	limited, err := store.GetDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: "scope.home", DeviceID: device.ID, AsOf: base.Add(30 * time.Minute), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !limited.Truncated || len(limited.Evidence) != 1 || limited.Evidence[0].Value != "192.168.1.20" {
		t.Fatalf("unexpected bounded detail: %+v", limited)
	}

	if _, err := store.GetDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: "scope.other", DeviceID: device.ID, AsOf: base.Add(30 * time.Minute)}); !errors.Is(err, ErrDeviceEvidenceNotFound) {
		t.Fatalf("cross-scope detail error = %v", err)
	}
}

func TestGetDeviceEvidenceDetailDoesNotResurrectExpiredObservationPayload(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return base }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)
	device := domain.Device{ID: "device.one", CreatedAt: base}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}

	observation := observationFixture("obs.short", "scope.home", "sensor.home", base)
	observation.Retention = domain.RetentionShort
	if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
		t.Fatalf("insert observation = %v, %v", inserted, err)
	}
	validUntil := base.Add(10 * time.Minute)
	confidence := 0.8
	claim := domain.IdentityClaim{ID: "claim.standard", ScopeID: "scope.home", Kind: domain.ClaimMAC, Value: "02:00:00:00:00:02", ObservedAt: base, ValidUntil: &validUntil, Confidence: &confidence, SourceSensorID: "sensor.home", SourceObservationID: observation.ID, Retention: domain.RetentionStandard}
	if err := store.InsertIdentityClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{ID: "link.standard", DeviceID: device.ID, ClaimID: claim.ID, ValidFrom: base, ValidUntil: &validUntil, Confidence: &confidence, Authority: domain.LinkInferred, Reason: "fixture", EvidenceObservationID: observation.ID, CreatedAt: base}); err != nil {
		t.Fatal(err)
	}

	store.now = func() time.Time { return base.Add(2 * 24 * time.Hour) }
	detail, err := store.GetDeviceEvidenceDetail(ctx, DeviceEvidenceDetailQuery{ScopeID: "scope.home", DeviceID: device.ID, AsOf: base.Add(2 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Evidence) != 1 || detail.Evidence[0].Observation != nil {
		t.Fatalf("expired source observation was resurrected: %+v", detail.Evidence)
	}
}

func seedDetailEvidence(t *testing.T, store *Store, scopeID, sensorID, deviceID, observationID, claimID, linkID string, kind domain.ClaimKind, value string, observedAt, validUntil time.Time) {
	t.Helper()
	ctx := context.Background()
	observation := observationFixture(observationID, scopeID, sensorID, observedAt)
	observation.SourceKey = observationID
	if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
		t.Fatalf("insert %s = %v, %v", observationID, inserted, err)
	}
	claimConfidence := 0.8
	claim := domain.IdentityClaim{ID: claimID, ScopeID: scopeID, Kind: kind, Value: value, ObservedAt: observedAt, ValidUntil: &validUntil, Confidence: &claimConfidence, SourceSensorID: sensorID, SourceObservationID: observationID, Retention: domain.RetentionStandard}
	if err := store.InsertIdentityClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	linkConfidence := 0.75
	if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{ID: linkID, DeviceID: deviceID, ClaimID: claimID, ValidFrom: observedAt, ValidUntil: &validUntil, Confidence: &linkConfidence, Authority: domain.LinkInferred, Reason: "device-watch:recent-mac-continuity:ip", EvidenceObservationID: observationID, CreatedAt: observedAt}); err != nil {
		t.Fatal(err)
	}
}
