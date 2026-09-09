package capability

import (
	"context"
	"testing"
)

func TestPreflightConfigurationDoesNotMutateDesiredState(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipBuiltin, DesiredDisabled)
	proposed := Configuration{ID: "lifecycle-test", Ownership: OwnershipBuiltin, Desired: DesiredEnabled}
	report, err := engine.PreflightConfiguration(context.Background(), proposed, ActionEnable)
	if err != nil || !report.Ready() || driver.preflightCalls != 1 {
		t.Fatalf("proposed preflight report=%+v calls=%d err=%v", report, driver.preflightCalls, err)
	}
	instances := engine.List()
	if len(instances) != 1 || instances[0].State.Desired != DesiredDisabled {
		t.Fatalf("proposed preflight mutated state: %+v", instances)
	}
}

func TestApplyConfigurationChangesDesiredAndInvalidatesVerification(t *testing.T) {
	engine, _ := newLifecycleHarness(t, OwnershipBuiltin, DesiredEnabled)
	if _, err := engine.Run(context.Background(), "lifecycle-test", ActionVerify); err != nil {
		t.Fatal(err)
	}
	before := engine.List()[0]
	if before.State.Verification != VerificationVerified {
		t.Fatalf("verification state before config change = %s", before.State.Verification)
	}

	disabled := Configuration{ID: "lifecycle-test", Ownership: OwnershipBuiltin, Desired: DesiredDisabled}
	if err := engine.ApplyConfiguration(disabled); err != nil {
		t.Fatal(err)
	}
	after := engine.List()[0]
	if after.State.Desired != DesiredDisabled || after.State.Verification != VerificationUnverified {
		t.Fatalf("configured runtime state = %+v", after.State)
	}
}

func TestRemoveConfigurationRestoresDefaultDisabledState(t *testing.T) {
	engine, _ := newLifecycleHarness(t, OwnershipBuiltin, DesiredEnabled)
	if err := engine.RemoveConfiguration("lifecycle-test"); err != nil {
		t.Fatal(err)
	}
	instance := engine.List()[0]
	if instance.Configured || instance.State.Desired != DesiredDisabled || instance.State.Verification != VerificationUnverified {
		t.Fatalf("default state after removal = %+v", instance)
	}
}

func TestPreflightConfigurationRejectsBlockedProposal(t *testing.T) {
	engine, driver := newLifecycleHarness(t, OwnershipBuiltin, DesiredDisabled)
	driver.preflight = PreflightReport{Checks: []PreflightCheck{{ID: "scope-current", Status: CheckFail, Blocking: true}}}
	proposed := Configuration{ID: "lifecycle-test", Ownership: OwnershipBuiltin, Desired: DesiredEnabled}
	if _, err := engine.PreflightConfiguration(context.Background(), proposed, ActionEnable); err == nil {
		t.Fatal("blocked proposed configuration was accepted")
	}
	if engine.List()[0].State.Desired != DesiredDisabled {
		t.Fatal("blocked proposed configuration mutated desired state")
	}
}
