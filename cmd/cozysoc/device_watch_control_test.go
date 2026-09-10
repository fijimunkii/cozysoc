package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/config"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type controlTestDriver struct {
	ready                bool
	active               bool
	failEnableAfterStart bool
	failDisable          bool
	closeCalls           int
}

func (d *controlTestDriver) Preflight(context.Context, capability.DriverRequest) (capability.PreflightReport, error) {
	status := capability.CheckPass
	if !d.ready {
		status = capability.CheckFail
	}
	return capability.PreflightReport{Checks: []capability.PreflightCheck{{ID: "ready", Status: status, Blocking: true}}}, nil
}

func (d *controlTestDriver) Execute(_ context.Context, action capability.LifecycleAction, _ capability.DriverRequest) (capability.StepResult, error) {
	switch action {
	case capability.ActionEnable:
		if d.active {
			return capability.StepResult{Changed: false}, nil
		}
		d.active = true
		if d.failEnableAfterStart {
			return capability.StepResult{}, errors.New("fixture enable failure")
		}
		return capability.StepResult{Changed: true}, nil
	case capability.ActionDisable:
		if d.failDisable {
			return capability.StepResult{}, errors.New("fixture disable failure")
		}
		if !d.active {
			return capability.StepResult{Changed: false}, nil
		}
		d.active = false
		return capability.StepResult{Changed: true}, nil
	default:
		return capability.StepResult{}, errors.New("unexpected lifecycle action")
	}
}

func (*controlTestDriver) Verify(context.Context, capability.DriverRequest) (capability.VerificationReport, error) {
	return capability.VerificationReport{Signals: []capability.VerificationSignal{
		{ID: "network-scope-enrolled", Status: capability.SignalFresh},
		{ID: "observation-freshness", Status: capability.SignalMissing},
	}}, nil
}

func (d *controlTestDriver) Active() bool { return d.active }

func (d *controlTestDriver) Close(context.Context) error {
	d.closeCalls++
	d.active = false
	return nil
}

func TestDeviceWatchControlPersistsLiveEnableDisableAndAudit(t *testing.T) {
	control, manager, driver, store := newDeviceWatchControlHarness(t, true)
	ctx := context.Background()

	enabled, err := control.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Changed || !enabled.Active || enabled.State.Desired != capability.DesiredEnabled || enabled.State.Verification != capability.VerificationUnverified {
		t.Fatalf("unexpected enable result: %+v", enabled)
	}
	configured, ok := manager.Capability(devicewatch.CapabilityID)
	if !ok || configured.Desired != capability.DesiredEnabled {
		t.Fatalf("enabled durable configuration = %+v ok=%v", configured, ok)
	}
	if count, err := store.CapabilityIntentAuditCount(ctx, devicewatch.CapabilityID); err != nil || count != 2 {
		t.Fatalf("enable audit count=%d err=%v", count, err)
	}

	again, err := control.Enable(ctx)
	if err != nil || again.Changed || !again.Active {
		t.Fatalf("idempotent enable result=%+v err=%v", again, err)
	}
	if count, _ := store.CapabilityIntentAuditCount(ctx, devicewatch.CapabilityID); count != 2 {
		t.Fatalf("idempotent enable added audit transitions: %d", count)
	}

	disabled, err := control.Disable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !disabled.Changed || disabled.Active || disabled.State.Desired != capability.DesiredDisabled {
		t.Fatalf("unexpected disable result: %+v", disabled)
	}
	configured, ok = manager.Capability(devicewatch.CapabilityID)
	if !ok || configured.Desired != capability.DesiredDisabled {
		t.Fatalf("disabled durable configuration = %+v ok=%v", configured, ok)
	}
	if driver.active {
		t.Fatal("runtime remained active after disable")
	}
	if count, err := store.CapabilityIntentAuditCount(ctx, devicewatch.CapabilityID); err != nil || count != 4 {
		t.Fatalf("enable+disable audit count=%d err=%v", count, err)
	}
}

func TestDeviceWatchControlBlockedEnableDoesNotPersistIntent(t *testing.T) {
	control, manager, driver, store := newDeviceWatchControlHarness(t, false)
	_, err := control.Enable(context.Background())
	if !errors.Is(err, localapi.ErrMutationPrecondition) {
		t.Fatalf("blocked enable error = %v", err)
	}
	if _, ok := manager.Capability(devicewatch.CapabilityID); ok {
		t.Fatal("blocked enable persisted capability intent")
	}
	if driver.active {
		t.Fatal("blocked enable activated runtime")
	}
	if count, _ := store.CapabilityIntentAuditCount(context.Background(), devicewatch.CapabilityID); count != 0 {
		t.Fatalf("blocked preflight wrote %d intent audits", count)
	}
}

func TestDeviceWatchControlRollsBackFailedEnableAndStopsSideEffect(t *testing.T) {
	control, manager, driver, store := newDeviceWatchControlHarness(t, true)
	driver.failEnableAfterStart = true
	if _, err := control.Enable(context.Background()); err == nil {
		t.Fatal("failed enable unexpectedly succeeded")
	}
	if _, ok := manager.Capability(devicewatch.CapabilityID); ok {
		t.Fatal("failed enable left durable enabled configuration")
	}
	if driver.active {
		t.Fatal("failed enable left runtime active")
	}
	if driver.closeCalls == 0 {
		t.Fatal("failed enable did not invoke emergency runtime stop")
	}
	if count, _ := store.CapabilityIntentAuditCount(context.Background(), devicewatch.CapabilityID); count != 2 {
		t.Fatalf("failed enable audit count = %d, want requested+failed", count)
	}
}

func TestDeviceWatchControlFailedDisableStaysDurablyDisabled(t *testing.T) {
	control, manager, driver, store := newDeviceWatchControlHarness(t, true)
	if _, err := control.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	driver.failDisable = true
	if _, err := control.Disable(context.Background()); err == nil {
		t.Fatal("failed disable unexpectedly succeeded")
	}
	configured, ok := manager.Capability(devicewatch.CapabilityID)
	if !ok || configured.Desired != capability.DesiredDisabled {
		t.Fatalf("failed disable did not preserve disabled durable intent: %+v ok=%v", configured, ok)
	}
	if driver.active {
		t.Fatal("emergency stop did not contain failed disable")
	}
	if count, _ := store.CapabilityIntentAuditCount(context.Background(), devicewatch.CapabilityID); count != 4 {
		t.Fatalf("failed disable audit count = %d, want enable requested/applied + disable requested/failed", count)
	}
}

func newDeviceWatchControlHarness(t *testing.T, ready bool) (*deviceWatchControl, *config.Manager, *controlTestDriver, *storage.Store) {
	t.Helper()
	dir := t.TempDir()
	registry, err := capability.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	initial, err := config.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(dir, registry, initial)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := capability.NewInstances(registry, initial.Capabilities)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	metadata, err := devicewatch.EncodeScopeMetadata(devicewatch.ScopeBinding{
		InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.EnrollDeviceWatchScope(context.Background(), metadata); err != nil || !changed {
		t.Fatalf("fixture scope enrollment changed=%v err=%v", changed, err)
	}
	driver := &controlTestDriver{ready: ready}
	lifecycle, err := capability.NewLifecycleEngine(instances, map[string]capability.LifecycleDriver{
		devicewatch.CapabilityID: driver,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	control, err := newDeviceWatchControl(manager, lifecycle, driver, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	return control, manager, driver, store
}

func rawScopeValue(t *testing.T, scopeID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(scopeID)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
