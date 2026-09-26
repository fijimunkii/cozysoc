package opnsense

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type routerCollectorProbe struct {
	snapshot Snapshot
	reads    *int
	onRead   func()
}

func (p routerCollectorProbe) Probe(context.Context) (Status, error) { return p.snapshot.Status, nil }
func (p routerCollectorProbe) ReadNeighbors(context.Context) (Snapshot, error) {
	*p.reads++
	if p.onRead != nil {
		p.onRead()
	}
	return p.snapshot, nil
}

type routerCollectorInspector struct{ state devicewatch.InterfaceState }

func (i routerCollectorInspector) Inspect(context.Context, string) (devicewatch.InterfaceState, error) {
	return i.state, nil
}

type routerRejectingAudit struct{ *storage.Store }

func (routerRejectingAudit) InsertAuditEvent(context.Context, domain.AuditEvent) error {
	return errors.New("private audit failure")
}

func TestRouterCollectorRequiresReviewedScopeAndStoresOnlyRouterEvidence(t *testing.T) {
	ctx := context.Background()
	connection, _, _, _, _, _ := newConnectionFixture(t)
	if _, err := connection.Connect(ctx, "https://192.168.1.1", "private-api-key", secretstore.NewSecret([]byte("private-api-secret")), []byte("private-certificate")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	reads := 0
	var onRead func()
	snapshot := Snapshot{Status: Status{Version: SupportedVersion}, IPv4Total: 2, IPv6Total: 1, Neighbors: []Neighbor{
		{IP: netip.MustParseAddr("192.168.1.10"), MAC: "02:00:00:00:00:10", Interface: "igb0", Family: "ipv4"},
		{IP: netip.MustParseAddr("192.168.2.10"), MAC: "02:00:00:00:00:11", Interface: "igb1", Family: "ipv4"},
		{IP: netip.MustParseAddr("fd00:1::10"), MAC: "02:00:00:00:00:12", Interface: "igb0", Family: "ipv6"},
	}}
	connection.probe = func(string, string, secretstore.Secret, []byte) (serviceProbe, error) {
		return routerCollectorProbe{snapshot: snapshot, reads: &reads, onRead: onRead}, nil
	}
	dir := t.TempDir()
	store, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	binding := devicewatch.ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24", "fd00:1::/64"}}
	metadata, err := devicewatch.EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.home", Kind: "lan", EnrolledAt: now.Add(-time.Hour), Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	ingestor, err := storage.NewEvidenceBatchIngestor(store, 16, nil, devicewatch.PlanBatchEvidence, devicewatch.RepairLegacyObservation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := ingestor.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	inspector := routerCollectorInspector{state: devicewatch.InterfaceState{Name: "en0", Index: 7, Flags: net.FlagUp, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.1/24"), netip.MustParsePrefix("fd00:1::1/64")}}}
	collector, err := NewCollector(connection, store, ingestor, inspector)
	if err != nil {
		t.Fatal(err)
	}
	collector.now = func() time.Time { return now }
	if _, err := collector.CollectReviewed(ctx, "scope.missing", "https://192.168.1.1", binding); !errors.Is(err, ErrObservationScope) || reads != 0 {
		t.Fatalf("missing scope fetched router data: %v, reads %d", err, reads)
	}
	changed := binding
	changed.Prefixes = []string{"192.168.2.0/24"}
	if _, err := collector.CollectReviewed(ctx, "scope.home", "https://192.168.1.1", changed); !errors.Is(err, ErrObservationScope) || reads != 0 {
		t.Fatalf("changed binding fetched router data: %v, reads %d", err, reads)
	}
	if _, err := collector.CollectReviewed(ctx, "scope.home", "https://192.168.1.2", binding); !errors.Is(err, ErrConnectionChanged) || reads != 0 {
		t.Fatalf("changed endpoint fetched router data: %v, reads %d", err, reads)
	}
	denied, err := NewCollector(connection, routerRejectingAudit{store}, ingestor, inspector)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.CollectReviewed(ctx, "scope.home", "https://192.168.1.1", binding); !errors.Is(err, ErrObservationAudit) || reads != 0 {
		t.Fatalf("failed audit fetched router data: %v, reads %d", err, reads)
	}
	result, err := collector.CollectReviewed(ctx, "scope.home", "https://192.168.1.1", binding)
	if err != nil || result.Read != 3 || result.Inserted != 2 || result.SkippedOutside != 1 || reads != 1 {
		t.Fatalf("collection %+v, %v, reads %d", result, err, reads)
	}
	page, err := store.ListObservations(ctx, storage.ObservationQuery{ScopeID: "scope.home", Kind: NeighborObservationKind, Since: now.Add(-time.Minute), Until: now.Add(time.Minute), Limit: 10})
	if err != nil || len(page.Observations) != 2 {
		t.Fatalf("stored router evidence: %+v, %v", page, err)
	}
	for _, item := range page.Observations {
		if item.Attribution != "opnsense:neighbor-table;device-identity-unverified" {
			t.Fatalf("overstated attribution: %+v", item)
		}
		var payload struct {
			Address string `json:"address"`
		}
		if err := json.Unmarshal(item.Payload, &payload); err != nil || payload.Address == "192.168.2.10" {
			t.Fatalf("out-of-scope neighbor stored: %+v, %v", item, err)
		}
	}
	result, err = collector.CollectReviewed(ctx, "scope.home", "https://192.168.1.1", binding)
	if err != nil || result.Inserted != 0 || result.Deduplicated != 2 {
		t.Fatalf("same-bucket replay: %+v, %v", result, err)
	}
	onRead = func() {
		if _, err := store.RetireDeviceWatchScope(context.Background(), "scope.home"); err != nil {
			t.Errorf("retire scope during router read: %v", err)
		}
	}
	if _, err := collector.CollectReviewed(ctx, "scope.home", "https://192.168.1.1", binding); !errors.Is(err, ErrObservationScope) {
		t.Fatalf("scope retirement during read was accepted: %v", err)
	}
	auditDB, err := sql.Open("sqlite", filepath.Join(dir, storage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer auditDB.Close()
	rows, err := auditDB.QueryContext(ctx, `SELECT payload FROM audit_events WHERE kind='opnsense-collection'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	failedAfterRead := false
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(encoded, "192.168.1.10") || strings.Contains(encoded, "02:00:00:00:00:10") || strings.Contains(encoded, "private-api-secret") {
			t.Fatalf("private router data leaked to audit: %s", encoded)
		}
		var payload struct {
			Phase string `json:"phase"`
			Read  int    `json:"read"`
		}
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			t.Fatal(err)
		}
		failedAfterRead = failedAfterRead || (payload.Phase == "failed" && payload.Read == 3)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !failedAfterRead {
		t.Fatal("scope retirement did not audit the completed private read")
	}
}
