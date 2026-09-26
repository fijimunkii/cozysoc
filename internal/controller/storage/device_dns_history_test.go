package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestDeviceDNSHistoryRequiresUniqueTimeValidScopedIP(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	now := base.Add(6 * time.Minute)
	store.now = func() time.Time { return now }
	seedScopeAndSensor(t, store, "scope.home", "sensor.home", base)
	seedScopeAndSensor(t, store, "scope.other", "sensor.other", base)
	for _, item := range []struct{ scope, sensor string }{{"scope.home", "sensor.adguard.home"}, {"scope.other", "sensor.adguard.other"}} {
		if err := store.CreateSensor(ctx, domain.Sensor{ID: item.sensor, ScopeID: item.scope, Kind: "adguard-home", Ownership: "external", RegisteredAt: base, Metadata: json.RawMessage(`{"schema_version":1}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"device.one", "device.two"} {
		if err := store.CreateDevice(ctx, domain.Device{ID: id, CreatedAt: base.Add(-20 * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	seedDetailEvidence(t, store, "scope.home", "sensor.home", "device.one", "obs.ip.one", "claim.ip.one", "link.ip.one", domain.ClaimIPv4, "192.0.2.4", base.Add(4*time.Minute), base.Add(14*time.Minute))
	seedDetailEvidence(t, store, "scope.other", "sensor.other", "device.two", "obs.ip.other", "claim.ip.other", "link.ip.other", domain.ClaimIPv4, "192.0.2.4", base.Add(4*time.Minute), base.Add(14*time.Minute))

	insertDNS := func(id, scope, sensor, ip, name string, at time.Time) {
		t.Helper()
		payload, err := json.Marshal(dnsObservationPayload{SchemaVersion: 1, Name: name, QueryType: "A", ClientIP: ip, Filtering: "blocked"})
		if err != nil {
			t.Fatal(err)
		}
		observation := domain.Observation{ID: id, ScopeID: scope, SensorID: sensor, Kind: "dns-query-observed", SourceStream: "adguard-querylog-v1", SourceKey: id, SourceTime: &at, IngestedAt: now, SchemaVersion: 1, Attribution: "resolver-client-ip;device-identity-unverified", Payload: payload, Retention: domain.RetentionEphemeral}
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
			t.Fatal(inserted, err)
		}
	}
	insertDNS("obs.dns.match", "scope.home", "sensor.adguard.home", "192.0.2.4", "private.example", base.Add(5*time.Minute))
	insertDNS("obs.dns.other-scope", "scope.other", "sensor.adguard.other", "192.0.2.4", "other.example", base.Add(5*time.Minute))
	insertDNS("obs.dns.other-ip", "scope.home", "sensor.adguard.home", "192.0.2.5", "unmatched.example", base.Add(5*time.Minute))
	insertDNS("obs.dns.wrong-sensor", "scope.home", "sensor.home", "192.0.2.4", "forged.example", base.Add(5*time.Minute))

	query := DeviceEvidenceDetailQuery{ScopeID: "scope.home", DeviceID: "device.one", AsOf: now, Limit: MaxDeviceDetailEvidence}
	snapshot, err := store.GetDeviceDetailSnapshot(ctx, query)
	got := snapshot.DNSHistory
	if err != nil || len(got.Items) != 1 || got.Items[0].Name != "private.example" || got.Truncated {
		t.Fatalf("unique scoped history = %+v, %v", got, err)
	}

	seedDetailEvidence(t, store, "scope.home", "sensor.home", "device.two", "obs.ip.two", "claim.ip.two", "link.ip.two", domain.ClaimIPv4, "192.0.2.4", base.Add(-15*time.Minute), base.Add(14*time.Minute))
	snapshot, err = store.GetDeviceDetailSnapshot(ctx, query)
	got = snapshot.DNSHistory
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("ambiguous address was attributed: %+v, %v", got, err)
	}
}

func TestDeviceDNSClaimRejectsExpiredStaleAndUnverifiedAssociations(t *testing.T) {
	base := time.Unix(1_800_000_000, 0).UTC()
	valid := base.Add(10 * time.Minute)
	item := DeviceIdentityEvidence{Kind: domain.ClaimIPv4, Value: "192.0.2.4", ObservedAt: base, ClaimValidUntil: &valid, LinkValidUntil: &valid, Authority: domain.LinkInferred, Reason: "device-watch:new-mac-candidate:ip"}
	if !matchesDeviceDNSClaim([]DeviceIdentityEvidence{item}, "192.0.2.4", base.Add(5*time.Minute)) {
		t.Fatal("recent time-valid address was not associated")
	}
	for _, at := range []time.Time{base.Add(-time.Second), valid.Add(time.Second), base.Add(11 * time.Minute)} {
		if matchesDeviceDNSClaim([]DeviceIdentityEvidence{item}, "192.0.2.4", at) {
			t.Fatalf("invalid event time %s was associated", at)
		}
	}
	longValid := base.Add(time.Hour)
	stale := item
	stale.ClaimValidUntil, stale.LinkValidUntil = &longValid, &longValid
	if matchesDeviceDNSClaim([]DeviceIdentityEvidence{stale}, "192.0.2.4", base.Add(11*time.Minute)) {
		t.Fatal("old address observation was associated despite an extended validity timestamp")
	}
	for _, changed := range []DeviceIdentityEvidence{
		func() DeviceIdentityEvidence { copy := item; copy.LinkValidUntil = nil; return copy }(),
		func() DeviceIdentityEvidence { copy := item; copy.Authority = domain.LinkUser; return copy }(),
		func() DeviceIdentityEvidence { copy := item; copy.Reason = "unverified:ip"; return copy }(),
	} {
		if matchesDeviceDNSClaim([]DeviceIdentityEvidence{changed}, "192.0.2.4", base.Add(5*time.Minute)) {
			t.Fatalf("unverified evidence was associated: %+v", changed)
		}
	}
}

func TestDeviceDNSPayloadAcceptsAndNormalizesIPv6Spelling(t *testing.T) {
	payload := dnsObservationPayload{SchemaVersion: 1, Name: "private.example", QueryType: "AAAA", ClientIP: "2001:DB8::20", Filtering: "not-blocked"}
	if !validDNSHistoryPayload(payload) {
		t.Fatal("valid noncanonical IPv6 client spelling was rejected")
	}
	base := time.Unix(1_800_000_000, 0).UTC()
	valid := base.Add(10 * time.Minute)
	evidence := DeviceIdentityEvidence{Kind: domain.ClaimIPv6, Value: "2001:db8::20", ObservedAt: base, ClaimValidUntil: &valid, LinkValidUntil: &valid, Authority: domain.LinkInferred, Reason: "device-watch:new-mac-candidate:ip"}
	if !matchesDeviceDNSClaim([]DeviceIdentityEvidence{evidence}, payload.ClientIP, base.Add(time.Minute)) {
		t.Fatal("equivalent IPv6 spelling did not match retained identity")
	}
}

func TestDeviceDNSHistoryReadsCanonicalBatchObservations(t *testing.T) {
	store, db := detailBatchFixture(t)
	ctx := context.Background()
	identity := batchSQLRecord(1)
	at := *identity.Observation.SourceTime
	store.now = func() time.Time { return at.Add(6 * time.Minute) }
	identity.Links[1].Reason = "device-watch:recent-mac-continuity:ip"
	if inserted, err := batchSQLAppend(t, db, identity); err != nil || !inserted {
		t.Fatal(inserted, err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{ID: "sensor.adguard.fixture", ScopeID: "scope.fixture", Kind: "adguard-home", Ownership: "external", RegisteredAt: at, Metadata: json.RawMessage(`{"schema_version":1}`)}); err != nil {
		t.Fatal(err)
	}
	eventTime := at.Add(5 * time.Minute)
	expiresAt := eventTime.Add(24 * time.Hour)
	payload, err := json.Marshal(dnsObservationPayload{SchemaVersion: 1, Name: "private.example", QueryType: "A", ClientIP: "192.168.50.10", Filtering: "blocked"})
	if err != nil {
		t.Fatal(err)
	}
	dns := EvidenceBatchRecord{Observation: &domain.Observation{ID: "obs.adguard.fixture", ScopeID: "scope.fixture", SensorID: "sensor.adguard.fixture", Kind: "dns-query-observed", SourceStream: "adguard-querylog-v1", SourceKey: "opaque.fixture", SourceTime: &eventTime, IngestedAt: at.Add(6 * time.Minute), SchemaVersion: 1, Attribution: "resolver-client-ip;device-identity-unverified", Payload: payload, Retention: domain.RetentionEphemeral}, ObservationExpiresAt: &expiresAt}
	if inserted, err := batchSQLAppend(t, db, dns); err != nil || !inserted {
		t.Fatal(inserted, err)
	}
	snapshot, err := store.GetDeviceDetailSnapshot(ctx, DeviceEvidenceDetailQuery{ScopeID: "scope.fixture", DeviceID: "device.fixture", AsOf: at.Add(6 * time.Minute), Limit: MaxDeviceDetailEvidence})
	if err != nil || len(snapshot.DNSHistory.Items) != 1 || snapshot.DNSHistory.Items[0].Name != "private.example" || len(snapshot.Detail.Evidence) != 2 {
		t.Fatalf("canonical batch DNS history = %+v, %v", snapshot, err)
	}
}
