package devicewatch

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type fakeRuntimeControl struct {
	running    bool
	scopeID    string
	startCalls int
	stopCalls  int
}

func (f *fakeRuntimeControl) Start(_ context.Context, scopeID string) error {
	f.running = true
	f.scopeID = scopeID
	f.startCalls++
	return nil
}

func (f *fakeRuntimeControl) Stop(context.Context) (bool, error) {
	f.stopCalls++
	if !f.running {
		return false, nil
	}
	f.running = false
	return true, nil
}

func (f *fakeRuntimeControl) Running() bool { return f.running }
func (f *fakeRuntimeControl) State() RuntimeState {
	return RuntimeState{Running: f.running, ScopeID: f.scopeID}
}

func TestLifecycleDriverPreflightAndRuntimeTransitions(t *testing.T) {
	store, inspector := lifecycleScopeFixture(t)
	runtime := &fakeRuntimeControl{}
	driver, err := newLifecycleDriver(store, runtime, inspector, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	enabled := lifecycleConfiguration(t, capability.DesiredEnabled)
	request := capability.DriverRequest{Configuration: enabled, Ownership: capability.OwnershipBuiltin, Action: capability.ActionEnable}

	report, err := driver.Preflight(context.Background(), request)
	if err != nil || !report.Ready() {
		t.Fatalf("enable preflight ready=%v err=%v report=%+v", report.Ready(), err, report)
	}
	step, err := driver.Execute(context.Background(), capability.ActionEnable, request)
	if err != nil || !step.Changed || runtime.startCalls != 1 || !runtime.running {
		t.Fatalf("enable step=%+v err=%v runtime=%+v", step, err, runtime)
	}
	step, err = driver.Execute(context.Background(), capability.ActionEnable, request)
	if err != nil || step.Changed || runtime.startCalls != 1 {
		t.Fatalf("idempotent enable step=%+v err=%v runtime=%+v", step, err, runtime)
	}

	disabled := lifecycleConfiguration(t, capability.DesiredDisabled)
	disableRequest := capability.DriverRequest{Configuration: disabled, Ownership: capability.OwnershipBuiltin, Action: capability.ActionDisable}
	report, err = driver.Preflight(context.Background(), disableRequest)
	if err != nil || !report.Ready() {
		t.Fatalf("disable preflight ready=%v err=%v report=%+v", report.Ready(), err, report)
	}
	step, err = driver.Execute(context.Background(), capability.ActionDisable, disableRequest)
	if err != nil || !step.Changed || runtime.running || runtime.stopCalls != 1 {
		t.Fatalf("disable step=%+v err=%v runtime=%+v", step, err, runtime)
	}
}

func TestLifecycleDriverFailsEnablePreflightOnUnsupportedPlatform(t *testing.T) {
	store, inspector := lifecycleScopeFixture(t)
	driver, err := newLifecycleDriver(store, nil, inspector, "linux")
	if err != nil {
		t.Fatal(err)
	}
	report, err := driver.Preflight(context.Background(), capability.DriverRequest{
		Configuration: lifecycleConfiguration(t, capability.DesiredEnabled),
		Ownership:     capability.OwnershipBuiltin,
		Action:        capability.ActionEnable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready() || len(report.Checks) != 1 || report.Checks[0].ID != "platform-supported" || report.Checks[0].Status != capability.CheckFail {
		t.Fatalf("unsupported platform preflight = %+v", report)
	}
}

func TestLifecycleDriverNeverRequiresNetworkPresenceToDisable(t *testing.T) {
	store, _ := lifecycleScopeFixture(t)
	runtime := &fakeRuntimeControl{running: true, scopeID: "scope.home"}
	mismatch := fakeInspector{state: InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/24")},
	}}
	driver, err := newLifecycleDriver(store, runtime, mismatch, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	report, err := driver.Preflight(context.Background(), capability.DriverRequest{
		Configuration: lifecycleConfiguration(t, capability.DesiredDisabled),
		Ownership:     capability.OwnershipBuiltin,
		Action:        capability.ActionDisable,
	})
	if err != nil || !report.Ready() {
		t.Fatalf("disable was blocked by network mismatch: report=%+v err=%v", report, err)
	}
}

func TestLifecycleVerifyKeepsObservationFreshnessMissing(t *testing.T) {
	store, inspector := lifecycleScopeFixture(t)
	driver, err := newLifecycleDriver(store, &fakeRuntimeControl{}, inspector, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	report, err := driver.Verify(context.Background(), capability.DriverRequest{
		Configuration: lifecycleConfiguration(t, capability.DesiredEnabled),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Signals) != 2 || report.Signals[0].Status != capability.SignalFresh || report.Signals[1].Status != capability.SignalMissing {
		t.Fatalf("unexpected verification report: %+v", report)
	}
}

func lifecycleScopeFixture(t *testing.T) (*storage.Store, fakeInspector) {
	t.Helper()
	store, err := storage.Open(t.TempDir(), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	binding := ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	metadata, err := EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNetworkScope(context.Background(), domain.NetworkScope{
		ID: "scope.home", Kind: "lan", EnrolledAt: time.Now().UTC(), Metadata: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	return store, fakeInspector{state: InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.42/24")},
	}}
}

func lifecycleConfiguration(t *testing.T, desired capability.DesiredState) capability.Configuration {
	t.Helper()
	raw, err := json.Marshal("scope.home")
	if err != nil {
		t.Fatal(err)
	}
	return capability.Configuration{
		ID: CapabilityID, Ownership: capability.OwnershipBuiltin, Desired: desired,
		Values: map[string]json.RawMessage{"network_scope_id": raw},
	}
}
