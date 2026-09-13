package devicewatch

import (
	"context"
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
	SchemaVersion             int    `json:"schema_version"`
	Workload                  string `json:"workload"`
	OS                        string `json:"os"`
	Architecture              string `json:"architecture"`
	GoVersion                 string `json:"go_version"`
	SimulatedMinutes          int    `json:"simulated_minutes"`
	CollectionIntervalSeconds int    `json:"collection_interval_seconds"`
	Devices                   int    `json:"devices"`
	NewObservations           int64  `json:"new_observations"`
	DatabaseBytesBefore       int64  `json:"database_bytes_before"`
	DatabaseBytesAfter        int64  `json:"database_bytes_after"`
	DatabaseGrowthBytes       int64  `json:"database_growth_bytes"`
	WallElapsedMilliseconds   int64  `json:"wall_elapsed_ms"`
	DailyTargetBytes          int64  `json:"daily_target_bytes"`
	TargetComparison          string `json:"target_comparison"`
}

func TestDeviceWatchStorageWorkloadSmoke(t *testing.T) {
	report := runStorageWorkload(t, 3)
	if report.NewObservations != 300 || report.TargetComparison != "not-evaluated-short-run" {
		t.Fatalf("short run claimed daily evidence: %+v", report)
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
	report := runStorageWorkload(t, 24*60)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("storage-workload-report: %s", raw)
	// Completion means the measurement was collected and validated. The report's
	// target_comparison carries the budget result, independently of test success.
}

func runStorageWorkload(t *testing.T, rounds int) storageWorkloadReport {
	t.Helper()
	if rounds < 1 || rounds > 24*60 {
		t.Fatal("storage workload collection count is outside bounds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dir := t.TempDir()
	store, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ingestor, err := storage.NewIngestor(store, storage.DefaultIngestionCapacity, logger)
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
	reconciler, err := NewReconciler(store)
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewCollector(source, inspector, ReconcilingSink{Evidence: StorageSink{Ingestor: ingestor}, Reconciler: reconciler})
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
	wallStart := time.Now()
	for i := 0; i < rounds; i++ {
		source.snapshot.CapturedAt = start.Add(time.Duration(i) * time.Minute)
		collect()
		if rounds > 60 && (i+1)%60 == 0 {
			t.Logf("storage workload: %d/%d collections, database %d bytes", i+1, rounds, databaseBytes())
		}
	}
	count, err := store.ObservationCount(ctx)
	if err != nil || count != (rounds+1)*storageWorkloadDevices || source.calls != rounds+1 {
		t.Fatal("workload omitted or duplicated observations", count, err)
	}
	page, err := store.ListDevicesForScope(ctx, storage.DeviceQuery{ScopeID: "scope.storage-lab", AsOf: source.snapshot.CapturedAt, Limit: storageWorkloadDevices + 1})
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
	if after < before {
		t.Fatal("database unexpectedly shrank during retained growth workload")
	}
	reopened, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retained, err := reopened.ObservationCount(ctx)
	if err != nil || retained != count {
		t.Fatal("acknowledged workload did not survive reopen", err)
	}
	comparison := "not-evaluated-short-run"
	if rounds == 24*60 {
		comparison = "below-target-component-only"
		if after-before > 25<<20 {
			comparison = "exceeded"
		}
	}
	return storageWorkloadReport{SchemaVersion: 1, Workload: "stable-100-neighbor-storage-v1", OS: runtime.GOOS, Architecture: runtime.GOARCH, GoVersion: runtime.Version(), SimulatedMinutes: rounds, CollectionIntervalSeconds: 60, Devices: storageWorkloadDevices,
		NewObservations: int64(count - storageWorkloadDevices), DatabaseBytesBefore: before, DatabaseBytesAfter: after, DatabaseGrowthBytes: after - before,
		WallElapsedMilliseconds: time.Since(wallStart).Milliseconds(), DailyTargetBytes: 25 << 20, TargetComparison: comparison}
}
