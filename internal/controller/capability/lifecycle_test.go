package capability

import (
	"context"
	"errors"
	"testing"
)

type testLifecycleDriver struct {
	preflight    PreflightReport
	preflightErr error
	verification VerificationReport
	verifyErr    error
	execute      func(context.Context, LifecycleAction, DriverRequest) (StepResult, error)

	preflightCalls int
	executeCalls   int
	verifyCalls    int
}

func (d *testLifecycleDriver) Preflight(context.Context, DriverRequest) (PreflightReport, error) {
	d.preflightCalls++
	return d.preflight, d.preflightErr
}

func (d *testLifecycleDriver) Execute(ctx context.Context, action LifecycleAction, request DriverRequest) (StepResult, error) {
	d.executeCalls++
	if d.execute == nil {
		return StepResult{}, nil
	}
	return d.execute(ctx, action, request)
}

func (d *testLifecycleDriver) Verify(context.Context, DriverRequest) (VerificationReport, error) {
	d.verifyCalls++
	return d.verification, d.verifyErr
}

func newLifecycleHarness(t *testing.T, ownership OwnershipMode, desired DesiredState) (*LifecycleEngine, *testLifecycleDriver) {
	t.Helper()
	builtins, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	manifest, ok := builtins.Get("device-watch")
	if !ok {
		t.Fatal("device-watch builtin missing")
	}
	manifest.ID = "lifecycle-test"
	manifest.Ownership = []OwnershipMode{ownership}
	manifest.Config.Fields = nil
	manifest.Health.ProcessRequired = ownership != OwnershipBuiltin
	manifest.Health.VerificationSignals = []string{"process-ready", "data-fresh"}
	manifest.Lifecycle = []LifecycleAction{
		ActionPreflight,
		ActionInstall,
		ActionConnect,
		ActionStart,
		ActionEnable,
		ActionVerify,
		ActionDisable,
		ActionStop,
		ActionDisconnect,
		ActionUpgrade,
		ActionUninstall,
	}
	if ownership == OwnershipBuiltin {
		manifest.Lifecycle = []LifecycleAction{ActionPreflight, ActionEnable, ActionVerify, ActionDisable}
	}
	registry, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := NewInstances(registry, []Configuration{{
		ID:        manifest.ID,
		Ownership: ownership,
		Desired:   desired,
	}})
	if err != nil {
		t.Fatal(err)
	}
	driver := &testLifecycleDriver{
		preflight: PreflightReport{Checks: []PreflightCheck{{ID: "ready", Status: CheckPass, Blocking: true}}},
		verification: VerificationReport{Signals: []VerificationSignal{
			{ID: "process-ready", Status: SignalFresh},
			{ID: "data-fresh", Status: SignalFresh},
		}},
	}
	engine, err := NewLifecycleEngine(instances, map[string]LifecycleDriver{manifest.ID: driver}, nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.retryDelay = 0
	return engine, driver
}

func TestLifecyclePreflightBlocksMutation(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	driver.preflight = PreflightReport{Checks: []PreflightCheck{{ID: "permission", Status: CheckFail, Blocking: true, Message: "permission required"}}}

	result, err := engine.Run(context.Background(), "lifecycle-test", ActionStart)
	if !errors.Is(err, ErrPreflightBlocked) {
		t.Fatalf("preflight block error = %v", err)
	}
	if result.Preflight == nil || result.Preflight.Ready() {
		t.Fatalf("blocking preflight was not returned: %+v", result.Preflight)
	}
	if driver.executeCalls != 0 {
		t.Fatalf("blocked action executed %d times", driver.executeCalls)
	}
}

func TestLifecycleProcessStateDoesNotImplyVerification(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	running := ProcessRunning
	driver.execute = func(context.Context, LifecycleAction, DriverRequest) (StepResult, error) {
		return StepResult{Changed: true, Process: &running}, nil
	}

	result, err := engine.Run(context.Background(), "lifecycle-test", ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Process != ProcessRunning {
		t.Fatalf("process state not updated: %+v", result.State)
	}
	if result.State.Verification != VerificationUnverified {
		t.Fatalf("running process incorrectly implied verification: %+v", result.State)
	}
}

func TestLifecycleVerificationRequiresEveryDeclaredSignal(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	driver.verification = VerificationReport{Signals: []VerificationSignal{{ID: "process-ready", Status: SignalFresh}}}

	result, err := engine.Run(context.Background(), "lifecycle-test", ActionVerify)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Verification != VerificationUnverified {
		t.Fatalf("missing signal produced %q", result.State.Verification)
	}

	driver.verification = VerificationReport{Signals: []VerificationSignal{
		{ID: "process-ready", Status: SignalFresh},
		{ID: "data-fresh", Status: SignalStale},
	}}
	result, err = engine.Run(context.Background(), "lifecycle-test", ActionVerify)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Verification != VerificationStale {
		t.Fatalf("stale signal produced %q", result.State.Verification)
	}

	driver.verification = VerificationReport{Signals: []VerificationSignal{
		{ID: "process-ready", Status: SignalFresh},
		{ID: "data-fresh", Status: SignalFresh},
	}}
	result, err = engine.Run(context.Background(), "lifecycle-test", ActionVerify)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Verification != VerificationVerified {
		t.Fatalf("fresh required signals produced %q", result.State.Verification)
	}
}

func TestExternalOwnershipRejectsDestructiveLifecycleAction(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipExternal, DesiredEnabled)
	_, err := engine.Run(context.Background(), "lifecycle-test", ActionUpgrade)
	if !errors.Is(err, ErrOwnershipAction) {
		t.Fatalf("external upgrade error = %v", err)
	}
	if driver.preflightCalls != 0 || driver.executeCalls != 0 {
		t.Fatalf("external destructive action reached driver: preflight=%d execute=%d", driver.preflightCalls, driver.executeCalls)
	}
}

func TestLifecycleRetriesAreBounded(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	engine.maxAttempts = 3
	driver.execute = func(context.Context, LifecycleAction, DriverRequest) (StepResult, error) {
		return StepResult{}, Retryable(errors.New("temporary failure"))
	}

	result, err := engine.Run(context.Background(), "lifecycle-test", ActionStart)
	if err == nil {
		t.Fatal("retryable failure unexpectedly succeeded")
	}
	if result.Attempts != 3 || driver.executeCalls != 3 {
		t.Fatalf("retry attempts = %d, driver calls = %d", result.Attempts, driver.executeCalls)
	}
}

func TestLifecycleHonorsCancellationBeforeDriverCall(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := engine.Run(ctx, "lifecycle-test", ActionStart)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lifecycle error = %v", err)
	}
	if driver.preflightCalls != 0 || driver.executeCalls != 0 {
		t.Fatalf("canceled lifecycle reached driver: preflight=%d execute=%d", driver.preflightCalls, driver.executeCalls)
	}
}

func TestRepeatedLifecycleActionCanBeIdempotent(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	started := false
	running := ProcessRunning
	driver.execute = func(context.Context, LifecycleAction, DriverRequest) (StepResult, error) {
		changed := !started
		started = true
		return StepResult{Changed: changed, Process: &running}, nil
	}

	first, err := engine.Run(context.Background(), "lifecycle-test", ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Run(context.Background(), "lifecycle-test", ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || second.Changed {
		t.Fatalf("unexpected idempotency results: first=%v second=%v", first.Changed, second.Changed)
	}
	listed := engine.List()
	if len(listed) != 1 || listed[0].State.Process != ProcessRunning || listed[0].State.Verification != VerificationUnverified {
		t.Fatalf("runtime snapshot mismatch: %+v", listed)
	}
}

func TestLifecycleActionMustMatchDurableDesiredState(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredDisabled)
	_, err := engine.Run(context.Background(), "lifecycle-test", ActionStart)
	if !errors.Is(err, ErrDesiredStateMismatch) {
		t.Fatalf("desired-state mismatch error = %v", err)
	}
	if driver.preflightCalls != 0 || driver.executeCalls != 0 {
		t.Fatal("desired-state mismatch reached driver")
	}
}

func TestVerificationRejectsUndeclaredSignal(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipManagedLocal, DesiredEnabled)
	driver.verification = VerificationReport{Signals: []VerificationSignal{{ID: "made-up", Status: SignalFresh}}}
	_, err := engine.Run(context.Background(), "lifecycle-test", ActionVerify)
	if err == nil {
		t.Fatal("undeclared verification signal was accepted")
	}
}
