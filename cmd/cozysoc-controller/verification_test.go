package main

import (
	"context"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
)

type fakeDeviceWatchVerificationEngine struct {
	instances []capability.Instance
	calls     int
	action    capability.LifecycleAction
}

func (f *fakeDeviceWatchVerificationEngine) List() []capability.Instance {
	return append([]capability.Instance(nil), f.instances...)
}

func (f *fakeDeviceWatchVerificationEngine) Run(_ context.Context, id string, action capability.LifecycleAction) (capability.LifecycleResult, error) {
	if id != devicewatch.CapabilityID {
		return capability.LifecycleResult{}, nil
	}
	f.calls++
	f.action = action
	return capability.LifecycleResult{}, nil
}

func TestVerifyDeviceWatchRunsOnlyWhenEnabled(t *testing.T) {
	disabled := &fakeDeviceWatchVerificationEngine{instances: []capability.Instance{{
		Manifest: capability.Manifest{ID: devicewatch.CapabilityID},
		State:    capability.InstanceState{Desired: capability.DesiredDisabled},
	}}}
	if err := verifyDeviceWatch(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.calls != 0 {
		t.Fatalf("disabled Device Watch verification calls = %d", disabled.calls)
	}

	enabled := &fakeDeviceWatchVerificationEngine{instances: []capability.Instance{{
		Manifest: capability.Manifest{ID: devicewatch.CapabilityID},
		State:    capability.InstanceState{Desired: capability.DesiredEnabled},
	}}}
	if err := verifyDeviceWatch(context.Background(), enabled); err != nil {
		t.Fatal(err)
	}
	if enabled.calls != 1 || enabled.action != capability.ActionVerify {
		t.Fatalf("enabled verification calls=%d action=%s", enabled.calls, enabled.action)
	}
}
