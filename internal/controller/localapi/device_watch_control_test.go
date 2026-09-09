package localapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

type deviceWatchControlTestHandler struct {
	enableErr    error
	disableErr   error
	enableCalls  int
	disableCalls int
}

func (*deviceWatchControlTestHandler) Status() api.Status {
	return api.Status{APIVersion: api.Version, ControllerVersion: "test", Transport: "unix"}
}

func (*deviceWatchControlTestHandler) Health() api.Health {
	return api.Health{State: "ok", LastTickAt: time.Unix(1, 0).UTC()}
}

func (*deviceWatchControlTestHandler) Capabilities() api.CapabilityList {
	return api.CapabilityList{CatalogSchemaVersion: 1}
}

func (h *deviceWatchControlTestHandler) EnableDeviceWatch(context.Context) (api.DeviceWatchControlResult, error) {
	h.enableCalls++
	if h.enableErr != nil {
		return api.DeviceWatchControlResult{}, h.enableErr
	}
	return api.DeviceWatchControlResult{
		ScopeID: "scope.home", Changed: true, Active: true,
		State: capability.InstanceState{Desired: capability.DesiredEnabled, Process: capability.ProcessNotApplicable, Verification: capability.VerificationUnverified},
	}, nil
}

func (h *deviceWatchControlTestHandler) DisableDeviceWatch(context.Context) (api.DeviceWatchControlResult, error) {
	h.disableCalls++
	if h.disableErr != nil {
		return api.DeviceWatchControlResult{}, h.disableErr
	}
	return api.DeviceWatchControlResult{
		ScopeID: "scope.home", Changed: true, Active: false,
		State: capability.InstanceState{Desired: capability.DesiredDisabled, Process: capability.ProcessNotApplicable, Verification: capability.VerificationUnverified},
	}, nil
}

func TestDeviceWatchControlRoundTrip(t *testing.T) {
	handler := &deviceWatchControlTestHandler{}
	server := startMutationTestServer(t, handler)
	client := NewClient(server.stateDir)

	raw, err := client.Call(context.Background(), api.MethodDeviceWatchEnable)
	if err != nil {
		t.Fatal(err)
	}
	var enabled api.DeviceWatchControlResult
	if err := json.Unmarshal(raw, &enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.Changed || !enabled.Active || enabled.State.Desired != capability.DesiredEnabled || handler.enableCalls != 1 {
		t.Fatalf("unexpected enable result=%+v calls=%d", enabled, handler.enableCalls)
	}

	raw, err = client.Call(context.Background(), api.MethodDeviceWatchDisable)
	if err != nil {
		t.Fatal(err)
	}
	var disabled api.DeviceWatchControlResult
	if err := json.Unmarshal(raw, &disabled); err != nil {
		t.Fatal(err)
	}
	if !disabled.Changed || disabled.Active || disabled.State.Desired != capability.DesiredDisabled || handler.disableCalls != 1 {
		t.Fatalf("unexpected disable result=%+v calls=%d", disabled, handler.disableCalls)
	}
}

func TestDeviceWatchControlRejectsParams(t *testing.T) {
	server := startMutationTestServer(t, &deviceWatchControlTestHandler{})
	_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodDeviceWatchEnable, map[string]any{"surprise": true})
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("unexpected params error = %v", err)
	}
}

func TestDeviceWatchControlMapsPreconditionSafely(t *testing.T) {
	server := startMutationTestServer(t, &deviceWatchControlTestHandler{enableErr: ErrMutationPrecondition})
	_, err := NewClient(server.stateDir).Call(context.Background(), api.MethodDeviceWatchEnable)
	if err == nil || !strings.Contains(err.Error(), "precondition_failed") || strings.Contains(err.Error(), "scope.home") {
		t.Fatalf("unsafe precondition error = %v", err)
	}
}
