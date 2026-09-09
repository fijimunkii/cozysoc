package devicewatch

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type fakeSnapshotter struct {
	snapshot Snapshot
	err      error
	calls    int
}

func (f *fakeSnapshotter) Snapshot(context.Context, string) (Snapshot, error) {
	f.calls++
	return f.snapshot, f.err
}

type fakeEvidenceSink struct {
	seen         map[string]struct{}
	observations []domain.Observation
	coverage     []domain.CoverageSample
}

func (f *fakeEvidenceSink) PutObservation(_ context.Context, observation domain.Observation) (bool, error) {
	if f.seen == nil {
		f.seen = make(map[string]struct{})
	}
	if _, ok := f.seen[observation.SourceKey]; ok {
		return false, nil
	}
	f.seen[observation.SourceKey] = struct{}{}
	f.observations = append(f.observations, observation)
	return true, nil
}

func (f *fakeEvidenceSink) PutCoverageSample(_ context.Context, sample domain.CoverageSample) error {
	f.coverage = append(f.coverage, sample)
	return nil
}

func TestCollectorEmitsOnlyEnrolledNeighborsAndHonestCoverage(t *testing.T) {
	now := time.Unix(1_800_000_000, 123).UTC()
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24", "fe80::/64"}}
	inspector := fakeInspector{state: InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.42/24"), netip.MustParsePrefix("fe80::1234/64")},
	}}
	snapshotter := &fakeSnapshotter{snapshot: Snapshot{
		CapturedAt: now, InterfaceName: "en0",
		Sources: []SourceStatus{{Method: MethodARPCache, Available: true}, {Method: MethodNDPCache, Available: true}},
		Neighbors: []Neighbor{
			neighborFixture("192.168.1.10", "aa:bb:cc:dd:ee:01", "en0", MethodARPCache),
			neighborFixture("fe80::2%en0", "aa:bb:cc:dd:ee:02", "en0", MethodNDPCache),
			neighborFixture("10.0.0.9", "aa:bb:cc:dd:ee:03", "en0", MethodARPCache),
			neighborFixture("192.168.1.11", "aa:bb:cc:dd:ee:04", "en1", MethodARPCache),
		},
	}}
	sink := &fakeEvidenceSink{}
	collector, err := NewCollector(snapshotter, inspector, sink)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.CollectOnce(context.Background(), "scope.home", "sensor.desktop", binding)
	if err != nil {
		t.Fatal(err)
	}
	if result.Visible != 2 || result.Inserted != 2 || result.Deduplicated != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(sink.observations) != 2 || len(sink.coverage) != 1 {
		t.Fatalf("observations=%d coverage=%d", len(sink.observations), len(sink.coverage))
	}
	coverage := sink.coverage[0]
	if coverage.Status != "partial" {
		t.Fatalf("coverage status = %q", coverage.Status)
	}
	var evidence map[string]any
	if err := json.Unmarshal(coverage.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if got, ok := evidence["whole_network_traffic_visible"].(bool); !ok || got {
		t.Fatalf("coverage overstated traffic visibility: %+v", evidence)
	}
}

func TestCollectorDeduplicatesRepeatedMinuteBucket(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	inspector := fakeInspector{state: InterfaceState{Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24")}}}
	snapshotter := &fakeSnapshotter{snapshot: Snapshot{
		CapturedAt: now, InterfaceName: "en0", Sources: []SourceStatus{{Method: MethodARPCache, Available: true}},
		Neighbors: []Neighbor{neighborFixture("192.168.1.20", "aa:bb:cc:dd:ee:20", "en0", MethodARPCache)},
	}}
	sink := &fakeEvidenceSink{}
	collector, err := NewCollector(snapshotter, inspector, sink)
	if err != nil {
		t.Fatal(err)
	}
	first, err := collector.CollectOnce(context.Background(), "scope.home", "sensor.desktop", binding)
	if err != nil {
		t.Fatal(err)
	}
	second, err := collector.CollectOnce(context.Background(), "scope.home", "sensor.desktop", binding)
	if err != nil {
		t.Fatal(err)
	}
	if first.Inserted != 1 || second.Inserted != 0 || second.Deduplicated != 1 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if len(sink.observations) != 1 || len(sink.coverage) != 2 {
		t.Fatalf("observations=%d coverage=%d", len(sink.observations), len(sink.coverage))
	}
}

func TestCollectorRecordsUnavailableSourceWithoutInventingDepartures(t *testing.T) {
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	inspector := fakeInspector{state: InterfaceState{Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24")}}}
	snapshotter := &fakeSnapshotter{err: ErrSnapshotUnavailable}
	sink := &fakeEvidenceSink{}
	collector, err := NewCollector(snapshotter, inspector, sink)
	if err != nil {
		t.Fatal(err)
	}
	collector.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	_, err = collector.CollectOnce(context.Background(), "scope.home", "sensor.desktop", binding)
	if !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("snapshot error = %v", err)
	}
	if len(sink.observations) != 0 || len(sink.coverage) != 1 || sink.coverage[0].Status != "unavailable" {
		t.Fatalf("source gap emitted incorrect evidence: observations=%+v coverage=%+v", sink.observations, sink.coverage)
	}
}

func TestCollectorBlocksChangedNetworkBeforeSnapshot(t *testing.T) {
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	inspector := fakeInspector{state: InterfaceState{Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.10/24")}}}
	snapshotter := &fakeSnapshotter{}
	collector, err := NewCollector(snapshotter, inspector, &fakeEvidenceSink{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = collector.CollectOnce(context.Background(), "scope.home", "sensor.desktop", binding)
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("scope mismatch error = %v", err)
	}
	if snapshotter.calls != 0 {
		t.Fatalf("snapshotter called %d times after scope mismatch", snapshotter.calls)
	}
}

func neighborFixture(address, hardware, interfaceName string, method NeighborMethod) Neighbor {
	parsedAddress := netip.MustParseAddr(address)
	parsedHardware, err := net.ParseMAC(hardware)
	if err != nil {
		panic(err)
	}
	return Neighbor{Address: parsedAddress, HardwareAddr: parsedHardware, InterfaceName: interfaceName, Method: method}
}
