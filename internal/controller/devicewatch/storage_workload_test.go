package devicewatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const storageWorkloadDevices = 100

type storageWorkloadReport struct {
	SchemaVersion             int               `json:"schema_version"`
	Workload                  string            `json:"workload"`
	OS                        string            `json:"os"`
	Architecture              string            `json:"architecture"`
	GoVersion                 string            `json:"go_version"`
	SimulatedMinutes          int               `json:"simulated_minutes"`
	CollectionIntervalSeconds int               `json:"collection_interval_seconds"`
	Devices                   int               `json:"devices"`
	NewObservations           int64             `json:"new_observations"`
	ReopenedVisibleDevices    int               `json:"reopened_visible_devices,omitempty"`
	ReopenedClaims            int64             `json:"reopened_claims,omitempty"`
	ReopenedLinks             int64             `json:"reopened_links,omitempty"`
	DatabaseBytesBefore       int64             `json:"database_bytes_before"`
	DatabaseBytesAfter        int64             `json:"database_bytes_after"`
	DatabaseGrowthBytes       int64             `json:"database_growth_bytes"`
	WallElapsedMilliseconds   int64             `json:"wall_elapsed_ms"`
	DailyTargetBytes          int64             `json:"daily_target_bytes"`
	TargetComparison          string            `json:"target_comparison"`
	AllocationBefore          storageAllocation `json:"allocation_before"`
	AllocationAfter           storageAllocation `json:"allocation_after"`
}

func TestDeviceWatchStorageWorkloadSmoke(t *testing.T) {
	report := runStorageWorkload(t, 3, false)
	if report.NewObservations != 300 || report.TargetComparison != "not-evaluated-short-run" {
		t.Fatalf("short run claimed daily evidence: %+v", report)
	}
}

func TestCanonicalDeviceWatchStorageWorkloadSmoke(t *testing.T) {
	report := runStorageWorkload(t, 3, true)
	if report.NewObservations != 300 || report.TargetComparison != "not-evaluated-short-run" || report.ReopenedVisibleDevices != 100 || report.ReopenedClaims != 800 || report.ReopenedLinks != 800 {
		t.Fatalf("canonical short run lost evidence: %+v", report)
	}
}

// This opt-in workload advances observation timestamps, not the OS clock. It
// measures the real collector/ingestion/reconciliation/SQLite path with synthetic
// cache input. It is neither a 24-hour soak nor a CPU/RAM/controller benchmark.
func TestDeviceWatchDailyStorageWorkload(t *testing.T) {
	if os.Getenv("COZYSOC_STORAGE_WORKLOAD") == "" {
		t.Skip("set COZYSOC_STORAGE_WORKLOAD=1 for 100 devices and 1440 collections")
	}
	if os.Getenv("COZYSOC_STORAGE_WORKLOAD") != "1" {
		t.Fatal("COZYSOC_STORAGE_WORKLOAD must be exactly 1")
	}
	report := runStorageWorkload(t, 24*60, false)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("storage-workload-report: %s", raw)
	// Completion means the measurement was collected and validated. The report's
	// target_comparison carries the budget result, independently of test success.
}

func TestCanonicalDeviceWatchDailyStorageWorkload(t *testing.T) {
	if os.Getenv("COZYSOC_CANONICAL_STORAGE_WORKLOAD") == "" {
		t.Skip("set COZYSOC_CANONICAL_STORAGE_WORKLOAD=1 for 100 devices and 1440 collections")
	}
	if os.Getenv("COZYSOC_CANONICAL_STORAGE_WORKLOAD") != "1" {
		t.Fatal("COZYSOC_CANONICAL_STORAGE_WORKLOAD must be exactly 1")
	}
	report := runStorageWorkload(t, 24*60, true)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical-storage-workload-report: %s", raw)
}

func runStorageWorkload(t *testing.T, rounds int, canonical bool) storageWorkloadReport {
	t.Helper()
	if rounds < 1 || rounds > 24*60 {
		t.Fatal("storage workload collection count is outside bounds")
	}
	deadline := time.Minute
	if rounds == 24*60 {
		deadline = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	dir := t.TempDir()
	store, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var ingestor *storage.Ingestor
	if canonical {
		ingestor, err = storage.NewEvidenceBatchIngestor(store, storage.DefaultIngestionCapacity, logger, PlanBatchEvidence, RepairLegacyObservation)
	} else {
		ingestor, err = storage.NewIngestor(store, storage.DefaultIngestionCapacity, logger)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := ingestor.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	// Keep every source timestamp in the past. Default 7/30-day retention means
	// no workload row expires within this first simulated day. No limits, journal
	// settings or durability rules are changed to accelerate the experiment.
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Duration(rounds) * time.Minute)
	binding := ScopeBinding{InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}
	metadata, err := EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.storage-lab", Kind: "lan", EnrolledAt: start.Add(-time.Hour), Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSensor(ctx, domain.Sensor{ID: "sensor.storage-lab", ScopeID: "scope.storage-lab", Kind: "desktop-neighbor-cache", Ownership: "builtin", RegisteredAt: start.Add(-time.Hour), Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	source := &fakeSnapshotter{snapshot: Snapshot{CapturedAt: start.Add(-time.Minute), InterfaceName: binding.InterfaceName,
		Sources: []SourceStatus{{Method: MethodARPCache, Available: true}, {Method: MethodNDPCache, Available: true}}}}
	for i := 0; i < storageWorkloadDevices; i++ {
		source.snapshot.Neighbors = append(source.snapshot.Neighbors, neighborFixture(fmt.Sprintf("192.168.50.%d", i+10), fmt.Sprintf("02:00:00:00:00:%02x", i+1), binding.InterfaceName, MethodARPCache))
	}
	inspector := fakeInspector{state: InterfaceState{Name: binding.InterfaceName, Index: binding.InterfaceIndex, Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.50.250/24")}}}
	var sink EvidenceSink = StorageSink{Ingestor: ingestor}
	if !canonical {
		reconciler, err := NewReconciler(store)
		if err != nil {
			t.Fatal(err)
		}
		sink = ReconcilingSink{Evidence: sink, Reconciler: reconciler}
	}
	collector, err := NewCollector(source, inspector, sink)
	if err != nil {
		t.Fatal(err)
	}
	collect := func() {
		t.Helper()
		result, err := collector.CollectOnce(ctx, "scope.storage-lab", "sensor.storage-lab", binding)
		if err != nil || result.Visible != storageWorkloadDevices || result.Inserted != storageWorkloadDevices || result.Deduplicated != 0 {
			t.Fatalf("incomplete storage workload at %s: %+v %v", source.snapshot.CapturedAt, result, err)
		}
	}
	collect() // Establish the 100 represented devices before measuring growth.
	databaseBytes := func() int64 {
		t.Helper()
		info, err := os.Stat(filepath.Join(dir, storage.Filename))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("workload database unavailable", err)
		}
		return info.Size()
	}
	before := databaseBytes()
	allocationBefore := readStorageAllocation(t, filepath.Join(dir, storage.Filename))
	wallStart := time.Now()
	for i := 0; i < rounds; i++ {
		source.snapshot.CapturedAt = start.Add(time.Duration(i) * time.Minute)
		collect()
		if rounds > 60 && (i+1)%60 == 0 {
			t.Logf("storage workload: %d/%d collections, database %d bytes", i+1, rounds, databaseBytes())
		}
	}
	count, err := store.ObservationCount(ctx)
	if canonical {
		count, err = canonicalWorkloadLookupCount(ctx, store.Path())
	}
	if err != nil || count != (rounds+1)*storageWorkloadDevices || source.calls != rounds+1 {
		t.Fatal("workload omitted or duplicated observations", count, err)
	}
	var page storage.DevicePage
	if canonical {
		view, viewErr := storage.NewCanonicalEvidenceView(store)
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		page, err = view.ListDevicesForScope(ctx, storage.DeviceQuery{ScopeID: "scope.storage-lab", AsOf: source.snapshot.CapturedAt, Limit: storageWorkloadDevices + 1})
	} else {
		page, err = store.ListDevicesForScope(ctx, storage.DeviceQuery{ScopeID: "scope.storage-lab", AsOf: source.snapshot.CapturedAt, Limit: storageWorkloadDevices + 1})
	}
	if err != nil || len(page.Devices) != storageWorkloadDevices || page.NextID != "" {
		t.Fatal("stable neighbors did not preserve 100 represented devices", err)
	}
	if err := ingestor.Close(ctx); err != nil {
		t.Fatal(err)
	}
	stats := ingestor.Stats()
	if stats.Failed != 0 || stats.Dropped != 0 || stats.Rejected != 0 || stats.Depth != 0 || stats.Processed != uint64((rounds+1)*(storageWorkloadDevices+1)) {
		t.Fatalf("workload lost ingestion work: %+v", stats)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	after := databaseBytes()
	allocationAfter := readStorageAllocation(t, filepath.Join(dir, storage.Filename))
	if allocationBefore.PageSize*allocationBefore.PageCount != before || allocationAfter.PageSize*allocationAfter.PageCount != after {
		t.Fatal("page accounting differs from database file lengths")
	}
	if after < before {
		t.Fatal("database unexpectedly shrank during retained growth workload")
	}
	reopened, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retained, err := reopened.ObservationCount(ctx)
	var reopenedClaims, reopenedLinks int
	var reopenedVisible int
	if canonical {
		retained, reopenedClaims, reopenedLinks = verifyCanonicalWorkloadEvidence(ctx, t, reopened.Path())
		err = nil
		view, viewErr := storage.NewCanonicalEvidenceView(reopened)
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		presence, viewErr := ListPresence(ctx, view, "scope.storage-lab", source.snapshot.CapturedAt, "", storageWorkloadDevices+1)
		if viewErr != nil || len(presence.Devices) != storageWorkloadDevices || presence.NextID != "" {
			t.Fatal("canonical presence did not survive reopen", len(presence.Devices), viewErr)
		}
		for _, device := range presence.Devices {
			if device.State != PresenceVisible {
				t.Fatal("canonical presence changed after reopen", device)
			}
			reopenedVisible++
		}
	}
	if err != nil || retained != count {
		t.Fatal("acknowledged workload did not survive reopen", err)
	}
	if canonical && (reopenedClaims != count*2 || reopenedLinks != count*2) {
		t.Fatal("canonical identity evidence did not survive reopen", reopenedClaims, reopenedLinks)
	}
	comparison := "not-evaluated-short-run"
	workload := "stable-100-neighbor-storage-v1"
	if canonical {
		workload = "stable-100-neighbor-canonical-storage-v1"
	}
	if rounds == 24*60 {
		comparison = "within-target-component-only"
		if after-before > 30<<20 {
			comparison = "exceeded"
		}
	}
	schemaVersion, err := reopened.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return storageWorkloadReport{AllocationBefore: allocationBefore, AllocationAfter: allocationAfter, SchemaVersion: schemaVersion, Workload: workload, OS: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), SimulatedMinutes: rounds, CollectionIntervalSeconds: 60, Devices: storageWorkloadDevices,
		NewObservations: int64(count - storageWorkloadDevices), DatabaseBytesBefore: before, DatabaseBytesAfter: after, DatabaseGrowthBytes: after - before,
		ReopenedVisibleDevices: reopenedVisible, ReopenedClaims: int64(reopenedClaims), ReopenedLinks: int64(reopenedLinks),
		WallElapsedMilliseconds: time.Since(wallStart).Milliseconds(), DailyTargetBytes: 30 << 20, TargetComparison: comparison}
}

func canonicalWorkloadLookupCount(ctx context.Context, path string) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var count int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM evidence_batch_lookup").Scan(&count)
	return count, err
}

func verifyCanonicalWorkloadEvidence(ctx context.Context, t *testing.T, path string) (int, int, int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT data FROM evidence_batches ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var observations, claims, links int
	seenObservations := make(map[string]struct{})
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		records, err := storage.DecodeEvidenceBatch(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Observation == nil {
				t.Fatal("canonical workload lost an original observation")
			}
			if _, duplicate := seenObservations[record.Observation.ID]; duplicate {
				t.Fatal("canonical workload duplicated an observation", record.Observation.ID)
			}
			seenObservations[record.Observation.ID] = struct{}{}
			observations++
			claims += len(record.Claims)
			links += len(record.Links)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var lookup, coverage int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM evidence_batch_lookup").Scan(&lookup); err != nil || lookup != observations {
		t.Fatal("canonical lookup mismatch", lookup, observations, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM coverage_samples").Scan(&coverage); err != nil || coverage != observations/storageWorkloadDevices {
		t.Fatal("canonical coverage mismatch", coverage, observations, err)
	}
	return observations, claims, links
}
