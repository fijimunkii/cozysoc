package devicewatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestRuntimeFlowsPassiveNeighborIntoTemporalPresence(t *testing.T) {
	store, err := storage.Open(t.TempDir(), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ingestor, err := storage.NewIngestor(store, 8, slog.Default())
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = ingestor.Close(closeCtx)
		_ = store.Close()
	})

	now := time.Now().UTC().Truncate(time.Second)
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	metadata, err := EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{
		ID: "scope.home", Kind: "lan", EnrolledAt: now, Metadata: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	inspector := fakeInspector{state: InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.42/24")},
	}}
	snapshotter := &fakeSnapshotter{snapshot: Snapshot{
		CapturedAt: now, InterfaceName: "en0",
		Sources: []SourceStatus{{Method: MethodARPCache, Available: true}, {Method: MethodNDPCache}},
		Neighbors: []Neighbor{neighborFixture("192.168.1.10", "aa:bb:cc:dd:ee:01", "en0", MethodARPCache)},
	}}
	runtime, err := newRuntime(store, ingestor, slog.Default(), inspector, snapshotter, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runtime.now = func() time.Time { return now }
	if err := runtime.Start(ctx, "scope.home"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		state := runtime.State()
		if !state.LastSuccessfulAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime never completed initial collection: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	presence, err := ListPresence(ctx, store, "scope.home", now.Add(time.Minute), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(presence.Devices) != 1 {
		t.Fatalf("presence devices = %d, want 1", len(presence.Devices))
	}
	device := presence.Devices[0]
	if device.State != PresenceVisible || !device.FirstSeen.Equal(now) || !device.LastSeen.Equal(now) {
		t.Fatalf("unexpected device presence: %+v", device)
	}
	if snapshotter.calls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", snapshotter.calls)
	}
}

func TestPresenceBecomesUncertainWithoutFabricatedOffline(t *testing.T) {
	reader := fakeDeviceEvidenceReader{page: storage.DeviceEvidencePage{Devices: []storage.DeviceEvidenceSummary{{
		Device: domain.Device{ID: "device.one", CreatedAt: time.Unix(100, 0).UTC()},
		FirstSeen: time.Unix(100, 0).UTC(), LastSeen: time.Unix(200, 0).UTC(),
	}}}}
	page, err := ListPresence(context.Background(), reader, "scope.home", time.Unix(500, 0).UTC(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Devices) != 1 || page.Devices[0].State != PresenceUncertain {
		t.Fatalf("stale passive evidence was not uncertain: %+v", page)
	}
}

func TestEnabledScopeIDRequiresEnabledIntent(t *testing.T) {
	raw, _ := json.Marshal("scope.home")
	configs := []capability.Configuration{{
		ID: CapabilityID, Ownership: capability.OwnershipBuiltin, Desired: capability.DesiredEnabled,
		Values: map[string]json.RawMessage{"network_scope_id": raw},
	}}
	scopeID, enabled, err := EnabledScopeID(configs)
	if err != nil || !enabled || scopeID != "scope.home" {
		t.Fatalf("enabled scope = %q enabled=%v err=%v", scopeID, enabled, err)
	}
	configs[0].Desired = capability.DesiredDisabled
	if _, enabled, err := EnabledScopeID(configs); err != nil || enabled {
		t.Fatalf("disabled device watch became enabled: enabled=%v err=%v", enabled, err)
	}
}

type fakeDeviceEvidenceReader struct {
	page storage.DeviceEvidencePage
	err  error
}

func (f fakeDeviceEvidenceReader) ListDeviceEvidence(context.Context, storage.DeviceEvidenceQuery) (storage.DeviceEvidencePage, error) {
	return f.page, f.err
}
