package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type batchWorkloadSource struct{ snapshot devicewatch.Snapshot }

func (s *batchWorkloadSource) Snapshot(context.Context, string) (devicewatch.Snapshot, error) {
	return s.snapshot, nil
}

type batchWorkloadInspector struct{}

func (batchWorkloadInspector) Inspect(context.Context, string) (devicewatch.InterfaceState, error) {
	return devicewatch.InterfaceState{Name: "fixture0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.50.250/24")}}, nil
}

// Stable-fixture continuity replaces production identity queries, which have not
// been integrated. Devices and coverage use the real Store; bundles use the
// actual reserved adapter and codec with separate real-time stored expiries.
type batchWorkloadSink struct {
	store      *storage.Store
	db         *sql.DB
	reconciler *devicewatch.Reconciler
	current    storage.EvidenceBatchRecord
	devices    map[string]domain.Device
	macDevices map[string]string
	digest     hash.Hash
}

func (s *batchWorkloadSink) EnsureDevice(ctx context.Context, d domain.Device) error {
	if err := s.store.EnsureDevice(ctx, d); err != nil {
		return err
	}
	s.devices[d.ID] = d
	return nil
}
func (s *batchWorkloadSink) EnsureIdentityClaim(_ context.Context, c domain.IdentityClaim) (string, error) {
	s.current.Claims = append(s.current.Claims, storage.RetainedIdentityClaim{Claim: c, ExpiresAt: time.Now().UTC().Add(storage.DefaultLimits().Retention[c.Retention])})
	return c.ID, nil
}
func (s *batchWorkloadSink) EnsureDeviceClaimLink(_ context.Context, l domain.DeviceClaimLink) error {
	s.current.Links = append(s.current.Links, l)
	for _, c := range s.current.Claims {
		if c.Claim.ID == l.ClaimID && c.Claim.Kind == domain.ClaimMAC {
			s.macDevices[c.Claim.Value] = l.DeviceID
		}
	}
	return nil
}
func (s *batchWorkloadSink) FindRecentDevicesByClaim(_ context.Context, _ string, kind domain.ClaimKind, value string, _, _ time.Time) ([]domain.Device, error) {
	if kind != domain.ClaimMAC {
		return nil, fmt.Errorf("workload only supports stable MAC continuity")
	}
	if id := s.macDevices[value]; id != "" {
		return []domain.Device{s.devices[id]}, nil
	}
	return nil, nil
}
func (s *batchWorkloadSink) PutObservation(ctx context.Context, o domain.Observation) (bool, error) {
	expiry := time.Now().UTC().Add(storage.DefaultLimits().Retention[o.Retention])
	s.current = storage.EvidenceBatchRecord{Observation: &o, ObservationExpiresAt: &expiry}
	if _, err := s.reconciler.ReconcileObservation(ctx, o); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	inserted, err := storage.AppendEvidenceBatchForTest(ctx, tx, s.current)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if inserted {
		if err := hashWorkloadRecord(s.digest, s.current); err != nil {
			return false, err
		}
	}
	return inserted, nil
}
func (s *batchWorkloadSink) PutCoverageSample(ctx context.Context, c domain.CoverageSample) error {
	return s.store.InsertCoverageSample(ctx, c)
}
func hashWorkloadRecord(h hash.Hash, r storage.EvidenceBatchRecord) error {
	if r.Observation == nil {
		return fmt.Errorf("workload observation missing")
	}
	raw, err := json.Marshal(struct {
		Record  storage.EvidenceBatchRecord
		Payload []byte
	}{r, r.Observation.Payload})
	if err != nil {
		return err
	}
	_, err = h.Write(raw)
	return err
}

type batchWorkloadAllocation struct {
	PageSize, PageCount, FreePages int64
	Objects                        map[string]int64
}

func batchAllocation(t *testing.T, db *sql.DB) batchWorkloadAllocation {
	t.Helper()
	var a batchWorkloadAllocation
	a.Objects = map[string]int64{}
	for query, dest := range map[string]*int64{"PRAGMA page_size": &a.PageSize, "PRAGMA page_count": &a.PageCount, "PRAGMA freelist_count": &a.FreePages} {
		if err := db.QueryRow(query).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT name,sum(pgsize) FROM dbstat GROUP BY name ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			t.Fatal(err)
		}
		a.Objects[name] = size
		total += size
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if total+a.FreePages*a.PageSize != a.PageCount*a.PageSize {
		t.Fatal("unaccounted database pages")
	}
	return a
}

type batchWorkloadReport struct {
	Kind                 string                  `json:"kind"`
	Collections          int                     `json:"collections"`
	Devices              int                     `json:"devices"`
	NewObservations      int                     `json:"new_observations"`
	ReopenedObservations int                     `json:"reopened_observations"`
	ReopenedClaims       int                     `json:"reopened_claims"`
	ReopenedLinks        int                     `json:"reopened_links"`
	CoverageSamples      int                     `json:"coverage_samples"`
	BytesBefore          int64                   `json:"database_bytes_before"`
	BytesAfter           int64                   `json:"database_bytes_after"`
	Growth               int64                   `json:"database_growth_bytes"`
	DailyTarget          int64                   `json:"daily_target_bytes"`
	Comparison           string                  `json:"target_comparison"`
	SHA256               string                  `json:"reopened_evidence_sha256"`
	WallMS               int64                   `json:"wall_ms"`
	OS                   string                  `json:"os"`
	Architecture         string                  `json:"architecture"`
	Go                   string                  `json:"go_version"`
	SQLite               string                  `json:"sqlite_version"`
	Before               batchWorkloadAllocation `json:"allocation_before"`
	After                batchWorkloadAllocation `json:"allocation_after"`
}

func TestEvidenceBatchAdapterWorkloadSmoke(t *testing.T) {
	r := runBatchAdapterWorkload(t, 3)
	if r.NewObservations != 300 || r.Comparison != "not-evaluated-short-run" {
		t.Fatal("smoke claimed daily budget", r)
	}
}
func TestEvidenceBatchAdapterDailyFootprint(t *testing.T) {
	if os.Getenv("COZYSOC_BATCH_ADAPTER_WORKLOAD") == "" {
		t.Skip("set COZYSOC_BATCH_ADAPTER_WORKLOAD=1")
	}
	if os.Getenv("COZYSOC_BATCH_ADAPTER_WORKLOAD") != "1" {
		t.Fatal("COZYSOC_BATCH_ADAPTER_WORKLOAD must be exactly 1")
	}
	report := runBatchAdapterWorkload(t, 1440)
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("batch-adapter-report: %s", raw)
}
func runBatchAdapterWorkload(t *testing.T, rounds int) batchWorkloadReport {
	t.Helper()
	if rounds != 3 && rounds != 1440 {
		t.Fatal("unsupported workload count")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dir := t.TempDir()
	s, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Encoded literal path, rollback journal, FULL durability and explicit quota
	// on the dedicated writer; no vacuum, retention reduction or live network IO.
	uri := url.URL{Scheme: "file", Path: s.Path()}
	q := url.Values{"mode": {"rw"}, "_pragma": {"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(5000)", "max_page_count(262144)"}}
	uri.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	for query, want := range map[string]int64{"PRAGMA foreign_keys": 1, "PRAGMA synchronous": 2, "PRAGMA page_size": 4096, "PRAGMA max_page_count": 262144} {
		var got int64
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatal(query, got, err)
		}
	}
	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
		t.Fatal(journal, err)
	}
	if _, err := db.ExecContext(ctx, storage.EvidenceBatchSchemaForTest); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	binding := devicewatch.ScopeBinding{InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}
	metadata, err := devicewatch.EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.storage-lab", Kind: "lan", EnrolledAt: start.Add(-time.Hour), Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSensor(ctx, domain.Sensor{ID: "sensor.storage-lab", ScopeID: "scope.storage-lab", Kind: "desktop-neighbor-cache", Ownership: "builtin", RegisteredAt: start.Add(-time.Hour), Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	sink := &batchWorkloadSink{store: s, db: db, devices: map[string]domain.Device{}, macDevices: map[string]string{}, digest: sha256.New()}
	sink.reconciler, err = devicewatch.NewReconciler(sink)
	if err != nil {
		t.Fatal(err)
	}
	source := &batchWorkloadSource{snapshot: devicewatch.Snapshot{CapturedAt: start.Add(-time.Minute), InterfaceName: "fixture0", Sources: []devicewatch.SourceStatus{{Method: devicewatch.MethodARPCache, Available: true}, {Method: devicewatch.MethodNDPCache, Available: true}}}}
	for i := 0; i < 100; i++ {
		mac, err := net.ParseMAC(fmt.Sprintf("02:00:00:00:00:%02x", i+1))
		if err != nil {
			t.Fatal(err)
		}
		source.snapshot.Neighbors = append(source.snapshot.Neighbors, devicewatch.Neighbor{Address: netip.MustParseAddr(fmt.Sprintf("192.168.50.%d", i+10)), HardwareAddr: mac, InterfaceName: "fixture0", Method: devicewatch.MethodARPCache})
	}
	collector, err := devicewatch.NewCollector(source, batchWorkloadInspector{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	collect := func() {
		result, err := collector.CollectOnce(ctx, "scope.storage-lab", "sensor.storage-lab", binding)
		if err != nil || result.Visible != 100 || result.Inserted != 100 || result.Deduplicated != 0 {
			t.Fatal("incomplete adapter collection", result, err)
		}
	}
	collect()
	report := batchWorkloadReport{Kind: "reserved-batch-adapter-not-full-controller-budget", Collections: rounds, Devices: 100, NewObservations: rounds * 100, DailyTarget: 30 << 20, Comparison: "not-evaluated-short-run", OS: runtime.GOOS, Architecture: runtime.GOARCH, Go: runtime.Version()}
	report.Before = batchAllocation(t, db)
	report.BytesBefore = report.Before.PageSize * report.Before.PageCount
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&report.SQLite); err != nil {
		t.Fatal(err)
	}
	wallStart := time.Now()
	for i := 0; i < rounds; i++ {
		source.snapshot.CapturedAt = start.Add(time.Duration(i) * time.Minute)
		collect()
		if (i+1)%60 == 0 {
			t.Logf("batch adapter: %d/%d collections", i+1, rounds)
		}
	}
	if len(sink.devices) != 100 || len(sink.macDevices) != 100 {
		t.Fatal("continuity fixture changed device count")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	uri.RawQuery = "mode=ro"
	reopened, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetMaxOpenConns(1)
	defer reopened.Close()
	tx, err := reopened.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM evidence_batches ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if len(ids) > (rounds+1)*100 {
			t.Fatal("unbounded batch count")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	actual := sha256.New()
	for _, id := range ids {
		var data []byte
		var source, expiry int64
		var entries int
		if err := tx.QueryRowContext(ctx, "SELECT source_id,entries,next_expiry_ns,data FROM evidence_batches WHERE id=?", id).Scan(&source, &entries, &expiry, &data); err != nil {
			t.Fatal(err)
		}
		records, err := storage.DecodeEvidenceBatch(data)
		if err != nil || len(records) != entries || expiry != storage.NextEvidenceBatchExpiryForTest(records) {
			t.Fatal("invalid durable batch", err)
		}
		if err := storage.ValidateEvidenceBatchLookupsForTest(ctx, tx, id, source, records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if err := hashWorkloadRecord(actual, record); err != nil {
				t.Fatal(err)
			}
			report.ReopenedObservations++
			report.ReopenedClaims += len(record.Claims)
			report.ReopenedLinks += len(record.Links)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if report.ReopenedObservations != (rounds+1)*100 || report.ReopenedClaims != (rounds+1)*200 || report.ReopenedLinks != (rounds+1)*200 || !bytes.Equal(actual.Sum(nil), sink.digest.Sum(nil)) {
		t.Fatal("acknowledged evidence changed after reopen")
	}
	var devices int
	if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM devices").Scan(&devices); err != nil || devices != 100 {
		t.Fatal(devices, err)
	}
	if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM coverage_samples").Scan(&report.CoverageSamples); err != nil || report.CoverageSamples != rounds+1 {
		t.Fatal(report.CoverageSamples, err)
	}
	report.After = batchAllocation(t, reopened)
	report.BytesAfter = report.After.PageSize * report.After.PageCount
	info, err := os.Stat(s.Path())
	if err != nil || info.Size() != report.BytesAfter {
		t.Fatal("file differs from page accounting", err)
	}
	report.Growth = report.BytesAfter - report.BytesBefore
	report.SHA256 = hex.EncodeToString(actual.Sum(nil))
	report.WallMS = time.Since(wallStart).Milliseconds()
	if rounds == 1440 {
		report.Comparison = "above-target-before-query-integration"
		if report.Growth <= report.DailyTarget {
			report.Comparison = "within-target-before-query-integration"
		}
	}
	return report
}
