package storage

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestListObservationsIsScopeBoundedPaginatedAndExpiryAware(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return base }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)
	seedScopeAndSensor(t, store, "scope.lab", "sensor.lab", base)

	for _, fixture := range []struct {
		id        string
		scopeID   string
		sensorID  string
		ingested  time.Time
		retention domain.RetentionClass
	}{
		{"obs.old", "scope.home", "sensor.home", base.Add(time.Minute), domain.RetentionEphemeral},
		{"obs.home.1", "scope.home", "sensor.home", base.Add(2 * time.Minute), domain.RetentionStandard},
		{"obs.home.2", "scope.home", "sensor.home", base.Add(3 * time.Minute), domain.RetentionStandard},
		{"obs.lab", "scope.lab", "sensor.lab", base.Add(4 * time.Minute), domain.RetentionStandard},
	} {
		observation := observationFixture(fixture.id, fixture.scopeID, fixture.sensorID, fixture.ingested)
		observation.Retention = fixture.retention
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
			t.Fatalf("insert %s = %v, %v", fixture.id, inserted, err)
		}
	}

	store.now = func() time.Time { return base.Add(2 * time.Hour) }
	page, err := store.ListObservations(ctx, ObservationQuery{
		ScopeID: "scope.home",
		Since:   base,
		Until:   base.Add(3 * time.Hour),
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Observations) != 1 || page.Observations[0].ID != "obs.home.2" || page.Next == nil {
		t.Fatalf("unexpected first page: %+v", page)
	}
	second, err := store.ListObservations(ctx, ObservationQuery{
		ScopeID: "scope.home",
		Since:   base,
		Until:   base.Add(3 * time.Hour),
		Before:  page.Next,
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Observations) != 1 || second.Observations[0].ID != "obs.home.1" || second.Next != nil {
		t.Fatalf("unexpected second page: %+v", second)
	}
}

func TestObservationQueryRejectsUnboundedRequests(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	if _, err := store.ListObservations(ctx, ObservationQuery{
		ScopeID: "scope.home",
		Since:   base,
		Until:   base.Add(MaxQueryWindow + time.Second),
	}); err == nil {
		t.Fatal("oversized observation window was accepted")
	}
	if _, err := store.ListObservations(ctx, ObservationQuery{ScopeID: "scope.home", Limit: MaxQueryLimit + 1}); err == nil {
		t.Fatal("oversized observation page was accepted")
	}
	if _, err := store.ListObservations(ctx, ObservationQuery{}); err == nil {
		t.Fatal("cross-scope observation query without scope was accepted")
	}
}

func TestListDevicesForScopePreservesAmbiguousIdentity(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return base }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)

	observation := observationFixture("obs.identity", "scope.home", "sensor.home", base)
	if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
		t.Fatalf("insert observation = %v, %v", inserted, err)
	}
	claim := domain.IdentityClaim{
		ID:                  "claim.shared",
		ScopeID:             "scope.home",
		Kind:                domain.ClaimIPv4,
		Value:               "192.168.1.20",
		ObservedAt:          base,
		SourceSensorID:      "sensor.home",
		SourceObservationID: observation.ID,
		Retention:           domain.RetentionStandard,
	}
	if err := store.InsertIdentityClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	for _, device := range []domain.Device{
		{ID: "device.a", UserLabel: "Possible A", CreatedAt: base},
		{ID: "device.b", UserLabel: "Possible B", CreatedAt: base},
	} {
		if err := store.CreateDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
			ID:        "link." + device.ID[len("device."):],
			DeviceID:  device.ID,
			ClaimID:   claim.ID,
			ValidFrom: base,
			Authority: domain.LinkInferred,
			Reason:    "conflicting discovery evidence",
			CreatedAt: base,
		}); err != nil {
			t.Fatal(err)
		}
	}

	page, err := store.ListDevicesForScope(ctx, DeviceQuery{ScopeID: "scope.home", AsOf: base.Add(time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Devices) != 2 || page.Devices[0].ID != "device.a" || page.Devices[1].ID != "device.b" {
		t.Fatalf("ambiguous identity was collapsed: %+v", page.Devices)
	}
}

func TestListDevicesForScopeUsesTemporalLinkInterval(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return base }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)

	observation := observationFixture("obs.link", "scope.home", "sensor.home", base)
	if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
		t.Fatalf("insert observation = %v, %v", inserted, err)
	}
	validUntil := base.Add(time.Hour)
	claim := domain.IdentityClaim{
		ID:                  "claim.link",
		ScopeID:             "scope.home",
		Kind:                domain.ClaimMAC,
		Value:               "02:00:00:00:00:01",
		ObservedAt:          base,
		SourceSensorID:      "sensor.home",
		SourceObservationID: observation.ID,
		Retention:           domain.RetentionStandard,
	}
	if err := store.InsertIdentityClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	device := domain.Device{ID: "device.one", CreatedAt: base}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
		ID: "link.one", DeviceID: device.ID, ClaimID: claim.ID, ValidFrom: base, ValidUntil: &validUntil,
		Authority: domain.LinkInferred, Reason: "temporary association", CreatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}

	active, err := store.ListDevicesForScope(ctx, DeviceQuery{ScopeID: "scope.home", AsOf: base.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Devices) != 1 {
		t.Fatalf("active temporal link missing: %+v", active.Devices)
	}
	expired, err := store.ListDevicesForScope(ctx, DeviceQuery{ScopeID: "scope.home", AsOf: base.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(expired.Devices) != 0 {
		t.Fatalf("expired temporal link still active: %+v", expired.Devices)
	}
}
