package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type fakeDeviceStore struct {
	page           storage.DeviceEvidencePage
	err            error
	setScope       string
	setDevice      string
	setLabel       string
	setChanged     bool
	setErr         error
	activeScopes   []domain.NetworkScope
	activeErr      error
	enrollScope    domain.NetworkScope
	enrollChanged  bool
	enrollErr      error
	enrollMetadata json.RawMessage
}

func (f *fakeDeviceStore) ListDeviceEvidence(context.Context, storage.DeviceEvidenceQuery) (storage.DeviceEvidencePage, error) {
	return f.page, f.err
}

func (f *fakeDeviceStore) SetDeviceLabel(_ context.Context, scopeID, deviceID, label string) (bool, error) {
	f.setScope = scopeID
	f.setDevice = deviceID
	f.setLabel = label
	return f.setChanged, f.setErr
}

func (f *fakeDeviceStore) ListActiveDeviceWatchScopes(context.Context) ([]domain.NetworkScope, error) {
	return append([]domain.NetworkScope(nil), f.activeScopes...), f.activeErr
}

func (f *fakeDeviceStore) EnrollDeviceWatchScope(_ context.Context, metadata json.RawMessage) (domain.NetworkScope, bool, error) {
	f.enrollMetadata = append(json.RawMessage(nil), metadata...)
	return f.enrollScope, f.enrollChanged, f.enrollErr
}

type fakeDeviceWatchAPIControl struct {
	scopeID       string
	configured    bool
	currentErr    error
	enableResult  api.DeviceWatchControlResult
	enableErr     error
	disableResult api.DeviceWatchControlResult
	disableErr    error
	enableCalls   int
	disableCalls  int
}

func (f *fakeDeviceWatchAPIControl) Current() (string, bool, error) {
	return f.scopeID, f.configured, f.currentErr
}

func (f *fakeDeviceWatchAPIControl) Enable(context.Context) (api.DeviceWatchControlResult, error) {
	f.enableCalls++
	return f.enableResult, f.enableErr
}

func (f *fakeDeviceWatchAPIControl) Disable(context.Context) (api.DeviceWatchControlResult, error) {
	f.disableCalls++
	return f.disableResult, f.disableErr
}

type handlerInspector struct {
	states map[string]devicewatch.InterfaceState
}

func (h handlerInspector) Inspect(_ context.Context, name string) (devicewatch.InterfaceState, error) {
	state, ok := h.states[name]
	if !ok {
		return devicewatch.InterfaceState{}, errors.New("interface not found")
	}
	return state, nil
}

func TestControllerAPIHandlerListsConfiguredDevicePresence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &fakeDeviceStore{page: storage.DeviceEvidencePage{
		Devices: []storage.DeviceEvidenceSummary{
			{
				Device:    domain.Device{ID: "device.one", UserLabel: "Camera", CreatedAt: now.Add(-24 * time.Hour)},
				FirstSeen: now.Add(-24 * time.Hour),
				LastSeen:  now.Add(-time.Minute),
			},
			{
				Device:    domain.Device{ID: "device.two", CreatedAt: now.Add(-48 * time.Hour)},
				FirstSeen: now.Add(-48 * time.Hour),
				LastSeen:  now.Add(-10 * time.Minute),
			},
		},
		NextID: "device.three",
	}}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	list, err := handler.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !list.Configured || list.ScopeID != "scope.home" || !list.Truncated || len(list.Devices) != 2 {
		t.Fatalf("unexpected device list: %+v", list)
	}
	if list.Devices[0].UserLabel != "Camera" || list.Devices[0].State != "visible" {
		t.Fatalf("unexpected visible device: %+v", list.Devices[0])
	}
	if list.Devices[1].State != "uncertain" {
		t.Fatalf("stale device was not uncertain: %+v", list.Devices[1])
	}
}

func TestControllerAPIHandlerReturnsDisabledDeviceWatchState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), &fakeDeviceStore{}, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	list, err := handler.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list.Configured || list.ScopeID != "" || list.Truncated || len(list.Devices) != 0 || !list.AsOf.Equal(now) {
		t.Fatalf("unexpected disabled device list: %+v", list)
	}
}

func TestControllerAPIHandlerUsesLiveDeviceWatchState(t *testing.T) {
	store := &fakeDeviceStore{}
	control := &fakeDeviceWatchAPIControl{}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	if list, err := handler.Devices(context.Background()); err != nil || list.Configured {
		t.Fatalf("initial devices configured=%v err=%v", list.Configured, err)
	}
	control.scopeID = "scope.home"
	control.configured = true
	store.page = storage.DeviceEvidencePage{}
	if list, err := handler.Devices(context.Background()); err != nil || !list.Configured || list.ScopeID != "scope.home" {
		t.Fatalf("live devices state=%+v err=%v", list, err)
	}
}

func TestControllerAPIHandlerLabelsOnlyConfiguredScope(t *testing.T) {
	store := &fakeDeviceStore{setChanged: true}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.LabelDevice(context.Background(), api.DeviceLabelParams{DeviceID: "device.one", Label: stringPtr("Living Room TV")})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.DeviceID != "device.one" || result.UserLabel != "Living Room TV" {
		t.Fatalf("unexpected label result: %+v", result)
	}
	if store.setScope != "scope.home" || store.setDevice != "device.one" || store.setLabel != "Living Room TV" {
		t.Fatalf("mutation escaped configured scope: %+v", store)
	}
}

func TestControllerAPIHandlerRejectsInvalidAndUnavailableLabelTargets(t *testing.T) {
	store := &fakeDeviceStore{}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []api.DeviceLabelParams{
		{DeviceID: "device.one"},
		{DeviceID: "../device", Label: stringPtr("TV")},
		{DeviceID: "device.one", Label: stringPtr(" TV")},
	} {
		if _, err := handler.LabelDevice(context.Background(), params); !errors.Is(err, localapi.ErrInvalidMutation) {
			t.Fatalf("invalid params %+v error = %v", params, err)
		}
	}

	store.setErr = storage.ErrDeviceNotInScope
	if _, err := handler.LabelDevice(context.Background(), api.DeviceLabelParams{DeviceID: "device.one", Label: stringPtr("TV")}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatalf("out-of-scope error = %v", err)
	}

	control.configured = false
	control.scopeID = ""
	if _, err := handler.LabelDevice(context.Background(), api.DeviceLabelParams{DeviceID: "device.one", Label: stringPtr("TV")}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatalf("disabled mutation error = %v", err)
	}
}

func TestControllerAPIHandlerDelegatesDeviceWatchControl(t *testing.T) {
	control := &fakeDeviceWatchAPIControl{
		enableResult: api.DeviceWatchControlResult{ScopeID: "scope.home", Changed: true, Active: true, State: capability.InstanceState{Desired: capability.DesiredEnabled}},
		disableResult: api.DeviceWatchControlResult{ScopeID: "scope.home", Changed: true, Active: false, State: capability.InstanceState{Desired: capability.DesiredDisabled}},
	}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), &fakeDeviceStore{}, control)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := handler.EnableDeviceWatch(context.Background())
	if err != nil || !enabled.Active || enabled.State.Desired != capability.DesiredEnabled || control.enableCalls != 1 {
		t.Fatalf("enable result=%+v calls=%d err=%v", enabled, control.enableCalls, err)
	}
	disabled, err := handler.DisableDeviceWatch(context.Background())
	if err != nil || disabled.Active || disabled.State.Desired != capability.DesiredDisabled || control.disableCalls != 1 {
		t.Fatalf("disable result=%+v calls=%d err=%v", disabled, control.disableCalls, err)
	}
}

func TestControllerAPIHandlerListsNetworkCandidatesAndEnrollment(t *testing.T) {
	binding := devicewatch.ScopeBinding{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	metadata, err := devicewatch.EncodeScopeMetadata(binding)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &fakeDeviceStore{activeScopes: []domain.NetworkScope{{ID: "scope.home", Kind: "lan", EnrolledAt: now, Metadata: metadata}}}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.listScopeCandidates = func(context.Context, devicewatch.InterfaceInspector) ([]devicewatch.ScopeBinding, bool, error) {
		return []devicewatch.ScopeBinding{binding}, true, nil
	}

	list, err := handler.Networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !list.CandidatesTruncated || len(list.Candidates) != 1 || list.Candidates[0].InterfaceName != "en0" {
		t.Fatalf("unexpected network candidates: %+v", list)
	}
	if list.Enrolled == nil || list.Enrolled.ScopeID != "scope.home" || list.Enrolled.Interface.InterfaceIndex != 7 {
		t.Fatalf("unexpected enrolled network: %+v", list.Enrolled)
	}
}

func TestControllerAPIHandlerEnrollsCurrentInterfaceAndFailsClosed(t *testing.T) {
	state := devicewatch.InterfaceState{
		Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.42/24")},
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &fakeDeviceStore{
		enrollScope:   domain.NetworkScope{ID: "scope.generated", Kind: "lan", EnrolledAt: now},
		enrollChanged: true,
	}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.networkInspector = handlerInspector{states: map[string]devicewatch.InterfaceState{"en0": state}}

	result, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "en0"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.ScopeID != "scope.generated" || result.Interface.Prefixes[0] != "192.168.1.0/24" {
		t.Fatalf("unexpected enrollment result: %+v", result)
	}
	parsed, err := devicewatch.ParseScopeBinding(domain.NetworkScope{ID: "scope.generated", Metadata: store.enrollMetadata})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.InterfaceName != "en0" || parsed.InterfaceIndex != 7 {
		t.Fatalf("captured enrollment metadata changed: %+v", parsed)
	}

	if _, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "../bad"}); !errors.Is(err, localapi.ErrInvalidMutation) {
		t.Fatalf("invalid interface error = %v", err)
	}
	handler.networkInspector = handlerInspector{states: map[string]devicewatch.InterfaceState{
		"utun3": {Name: "utun3", Index: 8, Flags: net.FlagUp | net.FlagPointToPoint, Prefixes: []netip.Prefix{netip.MustParsePrefix("100.64.0.2/24")}},
	}}
	if _, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "utun3"}); !errors.Is(err, localapi.ErrMutationPrecondition) {
		t.Fatalf("point-to-point enrollment error = %v", err)
	}

	handler.networkInspector = handlerInspector{states: map[string]devicewatch.InterfaceState{"en0": state}}
	store.enrollErr = storage.ErrActiveDeviceWatchScopeExists
	if _, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "en0"}); !errors.Is(err, localapi.ErrMutationConflict) {
		t.Fatalf("conflicting enrollment error = %v", err)
	}
}

func stringPtr(value string) *string {
	return &value
}
