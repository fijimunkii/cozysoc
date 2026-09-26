package adguard

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestBuildObservationsAdmitsOnlyRecentScopedClientIPAndDeduplicates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := Snapshot{Status: Status{QueryLogEnabled: true}, Queries: []Query{
		{Time: now.Add(-time.Minute), Name: "private.example", Type: "A", ClientIP: "192.0.2.4", Status: "NOERROR", Reason: "FilteredBlackList", Filtering: "blocked"},
		{Time: now.Add(-time.Minute), Name: "private.example", Type: "A", ClientIP: "192.0.2.4", Status: "NOERROR", Reason: "FilteredBlackList", Filtering: "blocked"},
		{Time: now.Add(-time.Minute), Name: "other.example", Type: "A", ClientIP: "198.51.100.4", Status: "NOERROR", Reason: "", Filtering: "unknown"},
		{Time: now.Add(-time.Minute), Name: "anonymous.example", Type: "A", ClientID: "opaque", Status: "NOERROR", Reason: "", Filtering: "unknown"},
		{Time: now.Add(-25 * time.Hour), Name: "stale.example", Type: "A", ClientIP: "192.0.2.5", Status: "NOERROR", Reason: "", Filtering: "unknown"},
	}}
	prefix := netip.MustParsePrefix("192.0.2.0/24")
	observations, stats, err := BuildObservations(snapshot, "scope.home", "sensor.adguard.test", []netip.Prefix{prefix}, now)
	if err != nil || stats.Selected != 1 || stats.Duplicate != 1 || stats.OutsideScope != 1 || stats.WithoutClientIP != 1 || stats.OutsideWindow != 1 || len(observations) != 1 {
		t.Fatalf("observations=%d stats=%+v err=%v", len(observations), stats, err)
	}
	o := observations[0]
	if o.SourceTime == nil || !o.SourceTime.Equal(now.Add(-time.Minute)) || o.Retention != domain.RetentionEphemeral || o.Attribution != "resolver-client-ip;device-identity-unverified" || strings.Contains(o.ID, "private") || strings.Contains(o.SourceKey, "private") {
		t.Fatalf("unsafe observation %+v", o)
	}
	var payload struct {
		Name      string `json:"name"`
		ClientIP  string `json:"client_ip"`
		Filtering string `json:"filtering"`
	}
	if err := json.Unmarshal(o.Payload, &payload); err != nil || payload.Name != "private.example" || payload.ClientIP != "192.0.2.4" || payload.Filtering != "blocked" {
		t.Fatalf("payload %+v, %v", payload, err)
	}
	store, err := storage.Open(t.TempDir(), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.home", Kind: "lan", EnrolledAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{ID: "sensor.adguard.test", ScopeID: "scope.home", Kind: "adguard-home", Ownership: "external", RegisteredAt: now, Metadata: json.RawMessage(`{"schema_version":1}`)}); err != nil {
		t.Fatal(err)
	}
	for _, observation := range observations {
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || !inserted {
			t.Fatalf("first insert %v %v", inserted, err)
		}
		if inserted, err := store.InsertObservation(ctx, observation); err != nil || inserted {
			t.Fatalf("replayed insert %v %v", inserted, err)
		}
	}
	page, err := store.ListObservations(ctx, storage.ObservationQuery{ScopeID: "scope.home", Kind: QueryObservationKind, Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 10})
	if err != nil || len(page.Observations) != 1 {
		t.Fatalf("stored page %+v err %v", page, err)
	}
}

func TestBuildObservationsRejectsMalformedOrAnonymizedHistory(t *testing.T) {
	now := time.Now().UTC()
	prefix := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	query := Query{Time: now, Name: "private.example", Type: "A", ClientIP: "192.0.2.4", Status: "NOERROR", Reason: "", Filtering: "unknown"}
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
	}{
		{"disabled-with-queries", Snapshot{Queries: []Query{query}}},
		{"invalid-reason", Snapshot{Status: Status{QueryLogEnabled: true}, Queries: []Query{{Time: now, Name: "private.example", Type: "A", ClientIP: "192.0.2.4", Reason: "untrusted", Filtering: "unknown"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := BuildObservations(tc.snapshot, "scope.home", "sensor.adguard.test", prefix, now); err == nil {
				t.Fatal("invalid query was admitted")
			}
		})
	}
	anonymized := Snapshot{Status: Status{QueryLogEnabled: true, AnonymizedClients: true}, Queries: []Query{query}}
	observations, stats, err := BuildObservations(anonymized, "scope.home", "sensor.adguard.test", prefix, now)
	if err != nil || len(observations) != 0 || stats.WithoutClientIP != 1 {
		t.Fatalf("anonymized query admitted: %+v %+v %v", observations, stats, err)
	}
}
