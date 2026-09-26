package adguard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type collectorInspector struct{ state devicewatch.InterfaceState }

func (i collectorInspector) Inspect(context.Context, string) (devicewatch.InterfaceState, error) {
	return i.state, nil
}

type auditRejectingStore struct{ *storage.Store }

func (auditRejectingStore) InsertAuditEvent(context.Context, domain.AuditEvent) error {
	return errors.New("synthetic private failure")
}

func TestCollectorPersistsOnlyScopedQueryObservationsAndReplaysIdempotently(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	var requests atomic.Int32
	var retireOnQuery atomic.Bool
	var store *storage.Store
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/control/status":
			fmt.Fprint(w, `{"version":"v0.107.79","running":true,"protection_enabled":true}`)
		case "/control/filtering/status":
			fmt.Fprint(w, `{"enabled":true}`)
		case "/control/querylog/config":
			fmt.Fprint(w, `{"enabled":true,"anonymize_client_ip":false}`)
		case "/control/querylog?limit=100":
			if retireOnQuery.Load() {
				if _, err := store.RetireDeviceWatchScope(context.Background(), "scope.home"); err != nil {
					t.Errorf("retire scope during external read: %v", err)
				}
			}
			fmt.Fprintf(w, `{"data":[{"time":%q,"client":"192.0.2.4","question":{"name":"private.example","type":"A"},"status":"NOERROR","reason":"FilteredBlackList"},{"time":%q,"client":"198.51.100.4","question":{"name":"elsewhere.example","type":"A"},"status":"NOERROR","reason":"NotFilteredNotFound"}]}`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	config, audit, lifecycle := &memoryConfig{}, &memoryAudit{}, &memoryLifecycle{}
	connections, err := NewConnections(config, audit, lifecycle, func() (secretstore.Store, error) { return &memorySecrets{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Connect(ctx, server.URL, "", secretstore.Secret{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err = storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	binding := devicewatch.ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.0.2.0/24"}}
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
	inspector := collectorInspector{state: devicewatch.InterfaceState{Name: "en0", Index: 7, Flags: net.FlagUp, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}}}
	collector, err := NewCollector(connections, store, ingestor, inspector)
	if err != nil {
		t.Fatal(err)
	}
	requests.Store(0)
	if _, err := collector.Collect(ctx, "scope.missing"); err == nil || requests.Load() != 0 {
		t.Fatalf("unapproved scope fetched private history: %v, requests %d", err, requests.Load())
	}
	denied, err := NewCollector(connections, auditRejectingStore{store}, ingestor, inspector)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Collect(ctx, "scope.home"); !errors.Is(err, ErrObservationAudit) || requests.Load() != 0 {
		t.Fatalf("unconfirmed audit admitted private read: %v, requests %d", err, requests.Load())
	}
	changedBinding := binding
	changedBinding.Prefixes = []string{"198.51.100.0/24"}
	if _, err := collector.CollectReviewed(ctx, "scope.home", server.URL, changedBinding); !errors.Is(err, ErrObservationScope) || requests.Load() != 0 {
		t.Fatalf("changed reviewed scope fetched private history: %v, requests %d", err, requests.Load())
	}
	if _, err := collector.CollectReviewed(ctx, "scope.home", "http://127.0.0.1:9999", binding); !errors.Is(err, ErrConnectionChanged) || requests.Load() != 0 {
		t.Fatalf("changed reviewed endpoint fetched private history: %v, requests %d", err, requests.Load())
	}
	result, err := collector.CollectReviewed(ctx, "scope.home", server.URL, binding)
	if err != nil || result.Read != 2 || result.Inserted != 1 || result.Skipped.OutsideScope != 1 || !result.QueryLogEnabled {
		t.Fatalf("collection %+v, %v", result, err)
	}
	result, err = collector.Collect(ctx, "scope.home")
	if err != nil || result.Inserted != 0 || result.Deduplicated != 1 {
		t.Fatalf("replay %+v, %v", result, err)
	}
	retireOnQuery.Store(true)
	if _, err := collector.Collect(ctx, "scope.home"); !errors.Is(err, ErrObservationScope) {
		t.Fatalf("scope retirement during read was accepted: %v", err)
	}
	page, err := store.ListObservations(ctx, storage.ObservationQuery{ScopeID: "scope.home", Kind: QueryObservationKind, Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 10})
	if err != nil || len(page.Observations) != 1 {
		t.Fatalf("stored observations %+v, %v", page, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(page.Observations[0].Payload, &payload); err != nil || payload["name"] != "private.example" || payload["client_ip"] != "192.0.2.4" {
		t.Fatalf("stored payload %+v, %v", payload, err)
	}
	auditDB, err := sql.Open("sqlite", filepath.Join(dir, storage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer auditDB.Close()
	rows, err := auditDB.QueryContext(ctx, `SELECT payload FROM audit_events WHERE kind='adguard-collection' ORDER BY occurred_at_ns,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	audits := 0
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(encoded, "private.example") || strings.Contains(encoded, "192.0.2.4") || !strings.Contains(encoded, `"scope_id":"scope.home"`) {
			t.Fatalf("unsafe collection audit: %s", encoded)
		}
		audits++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if audits != 8 {
		t.Fatalf("got %d collection audit phases, want 8", audits)
	}
}
