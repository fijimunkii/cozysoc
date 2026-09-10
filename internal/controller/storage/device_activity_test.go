package storage

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestListDeviceActivityIsLowNoiseAndEvidenceDerived(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	asOf := base.Add(4 * time.Hour)
	store.now = func() time.Time { return asOf }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)

	one := domain.Device{ID: "device.one", UserLabel: "TV", CreatedAt: base}
	two := domain.Device{ID: "device.two", CreatedAt: base.Add(3 * time.Hour)}
	for _, device := range []domain.Device{one, two} {
		if err := store.CreateDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
	}
	seedActivityObservation(t, store, one.ID, "obs.one.first", base, "192.168.1.10", "02:00:00:00:00:01", domain.ClaimIPv4)
	seedActivityObservation(t, store, one.ID, "obs.one.repeat", base.Add(time.Hour), "192.168.1.10", "02:00:00:00:00:01", domain.ClaimIPv4)
	seedActivityObservation(t, store, one.ID, "obs.one.change", base.Add(2*time.Hour), "192.168.1.20", "02:00:00:00:00:01", domain.ClaimIPv4)
	seedActivityObservation(t, store, one.ID, "obs.one.latest", base.Add(3*time.Hour), "192.168.1.20", "02:00:00:00:00:01", domain.ClaimIPv4)
	seedActivityObservation(t, store, two.ID, "obs.two.first", two.CreatedAt, "192.168.1.30", "02:00:00:00:00:02", domain.ClaimIPv4)

	page, err := store.ListDeviceActivity(ctx, DeviceActivityQuery{ScopeID: "scope.home", AsOf: asOf, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Truncated || len(page.Items) != 4 {
		t.Fatalf("unexpected activity page: %+v", page)
	}
	want := []struct {
		id   string
		kind DeviceActivityKind
	}{
		{"obs.one.latest", DeviceActivityObserved},
		{"obs.two.first", DeviceActivityFirstObserved},
		{"obs.one.change", DeviceActivityAddressChanged},
		{"obs.one.first", DeviceActivityFirstObserved},
	}
	for index, expected := range want {
		if page.Items[index].ID != expected.id || page.Items[index].Kind != expected.kind {
			t.Fatalf("activity[%d] = %+v, want id=%s kind=%s", index, page.Items[index], expected.id, expected.kind)
		}
	}
	changed := page.Items[2]
	if changed.PreviousAddress != "192.168.1.10" || changed.Address != "192.168.1.20" || changed.UserLabel != "TV" {
		t.Fatalf("address change lost evidence: %+v", changed)
	}
	for _, item := range page.Items {
		if item.Source.ObservationID == "" || item.Source.SensorID != "sensor.home" || item.Source.Attribution != "device-watch:arp-cache" {
			t.Fatalf("activity source provenance is incomplete: %+v", item)
		}
	}
}

func TestListDeviceActivityDoesNotTreatDualStackAsAddressChange(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	asOf := base.Add(3 * time.Hour)
	store.now = func() time.Time { return asOf }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)
	device := domain.Device{ID: "device.dual", CreatedAt: base}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	seedActivityObservation(t, store, device.ID, "obs.dual.v4.first", base, "192.168.1.10", "02:00:00:00:00:03", domain.ClaimIPv4)
	seedActivityObservation(t, store, device.ID, "obs.dual.v6", base.Add(time.Hour), "2001:db8::10", "02:00:00:00:00:03", domain.ClaimIPv6)
	seedActivityObservation(t, store, device.ID, "obs.dual.v4.latest", base.Add(2*time.Hour), "192.168.1.10", "02:00:00:00:00:03", domain.ClaimIPv4)

	page, err := store.ListDeviceActivity(ctx, DeviceActivityQuery{ScopeID: "scope.home", AsOf: asOf, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Kind == DeviceActivityAddressChanged {
			t.Fatalf("dual-stack family transition was reported as address change: %+v", page.Items)
		}
	}
}

func TestListDeviceActivityIsBounded(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	asOf := base.Add(2 * time.Hour)
	store.now = func() time.Time { return asOf }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)
	for index := 0; index < 3; index++ {
		device := domain.Device{ID: "device.limit." + string(rune('a'+index)), CreatedAt: base.Add(time.Duration(index) * time.Minute)}
		if err := store.CreateDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
		seedActivityObservation(t, store, device.ID, "obs.limit."+string(rune('a'+index)), device.CreatedAt, "192.168.1."+string(rune('2'+index)), "02:00:00:00:00:0"+string(rune('4'+index)), domain.ClaimIPv4)
	}
	page, err := store.ListDeviceActivity(ctx, DeviceActivityQuery{ScopeID: "scope.home", AsOf: asOf, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Truncated || len(page.Items) != 2 {
		t.Fatalf("bounded page = %+v", page)
	}
}

func seedActivityObservation(t *testing.T, store *Store, deviceID, observationID string, at time.Time, address, hardware string, addressKind domain.ClaimKind) {
	t.Helper()
	ctx := context.Background()
	method := "arp-cache"
	if addressKind == domain.ClaimIPv6 {
		method = "ndp-cache"
	}
	observation := observationFixture(observationID, "scope.home", "sensor.home", at)
	observation.Kind = "device-neighbor-seen"
	observation.SourceStream = "device-watch-neighbors"
	observation.SourceKey = observationID
	observation.SourceEventID = observationID
	observation.SourceTime = &at
	observation.IngestedAt = at
	observation.Attribution = "device-watch:" + method
	if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
		t.Fatalf("insert %s = %v, %v", observationID, inserted, err)
	}
	validUntil := at.Add(10 * time.Minute)
	macConfidence := 0.9
	addressConfidence := 0.65
	claims := []domain.IdentityClaim{
		{ID: "claim.mac." + observationID, ScopeID: "scope.home", Kind: domain.ClaimMAC, Value: hardware, ObservedAt: at, ValidUntil: &validUntil, Confidence: &macConfidence, SourceSensorID: "sensor.home", SourceObservationID: observationID, Retention: domain.RetentionStandard},
		{ID: "claim.addr." + observationID, ScopeID: "scope.home", Kind: addressKind, Value: address, ObservedAt: at, ValidUntil: &validUntil, Confidence: &addressConfidence, SourceSensorID: "sensor.home", SourceObservationID: observationID, Retention: domain.RetentionStandard},
	}
	for index, claim := range claims {
		if err := store.InsertIdentityClaim(ctx, claim); err != nil {
			t.Fatal(err)
		}
		confidence := 0.8
		if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
			ID: "link." + observationID + "." + string(rune('a'+index)), DeviceID: deviceID, ClaimID: claim.ID,
			ValidFrom: at, ValidUntil: &validUntil, Confidence: &confidence, Authority: domain.LinkInferred,
			Reason: "device-watch:recent-mac-continuity", EvidenceObservationID: observationID, CreatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
}
