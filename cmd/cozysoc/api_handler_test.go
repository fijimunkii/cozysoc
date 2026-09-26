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
	page             storage.DeviceEvidencePage
	err              error
	detail           storage.DeviceEvidenceDetail
	detailErr        error
	detailQuery      storage.DeviceEvidenceDetailQuery
	dnsHistory       storage.DeviceDNSHistory
	dnsHistoryErr    error
	activity         storage.DeviceActivityPage
	activityErr      error
	activityQuery    storage.DeviceActivityQuery
	observations     storage.ObservationPage
	observationErr   error
	observationQuery storage.ObservationQuery
	setScope         string
	setDevice        string
	setLabel         string
	setChanged       bool
	setErr           error
	mergeScope       string
	mergeSource      string
	mergeTarget      string
	mergeChanged     bool
	mergeErr         error
	unmergeSource    string
	merges           []storage.DeviceMerge
	splitScope       string
	splitSource      string
	splitObs         string
	splitTarget      string
	splitChanged     bool
	splitErr         error
	splits           []storage.DeviceSplit
	activeScopes     []domain.NetworkScope
	activeErr        error
	enrollScope      domain.NetworkScope
	enrollChanged    bool
	enrollErr        error
	enrollMetadata   json.RawMessage
}

func (f *fakeDeviceStore) ListDeviceEvidence(context.Context, storage.DeviceEvidenceQuery) (storage.DeviceEvidencePage, error) {
	return f.page, f.err
}

func (f *fakeDeviceStore) GetDeviceEvidenceDetail(_ context.Context, query storage.DeviceEvidenceDetailQuery) (storage.DeviceEvidenceDetail, error) {
	f.detailQuery = query
	return f.detail, f.detailErr
}

func (f *fakeDeviceStore) GetDeviceDetailSnapshot(_ context.Context, query storage.DeviceEvidenceDetailQuery) (storage.DeviceDetailSnapshot, error) {
	f.detailQuery = query
	if f.detailErr != nil {
		return storage.DeviceDetailSnapshot{}, f.detailErr
	}
	return storage.DeviceDetailSnapshot{Detail: f.detail, DNSHistory: f.dnsHistory}, f.dnsHistoryErr
}

func (f *fakeDeviceStore) ListDeviceActivity(_ context.Context, query storage.DeviceActivityQuery) (storage.DeviceActivityPage, error) {
	f.activityQuery = query
	return f.activity, f.activityErr
}

func (f *fakeDeviceStore) ListObservations(_ context.Context, query storage.ObservationQuery) (storage.ObservationPage, error) {
	f.observationQuery = query
	return f.observations, f.observationErr
}

func (f *fakeDeviceStore) SetDeviceLabel(_ context.Context, scopeID, deviceID, label string) (bool, error) {
	f.setScope = scopeID
	f.setDevice = deviceID
	f.setLabel = label
	return f.setChanged, f.setErr
}

func (f *fakeDeviceStore) MergeDevices(_ context.Context, scope, source, target string) (bool, error) {
	f.mergeScope, f.mergeSource, f.mergeTarget = scope, source, target
	return f.mergeChanged, f.mergeErr
}

func (f *fakeDeviceStore) UnmergeDevices(_ context.Context, scope, source string) (bool, error) {
	f.mergeScope, f.unmergeSource = scope, source
	return f.mergeChanged, f.mergeErr
}

func (f *fakeDeviceStore) ListDeviceMerges(context.Context, string) ([]storage.DeviceMerge, error) {
	return f.merges, nil
}

func (f *fakeDeviceStore) SplitDeviceObservation(_ context.Context, scope, source, observation, target string) (string, bool, error) {
	f.splitScope, f.splitSource, f.splitObs, f.splitTarget = scope, source, observation, target
	if target == "" {
		target = "device.created"
	}
	return target, f.splitChanged, f.splitErr
}

func (f *fakeDeviceStore) UndoDeviceSplitObservation(_ context.Context, scope, observation string) (bool, error) {
	f.splitScope, f.splitObs = scope, observation
	return f.splitChanged, f.splitErr
}

func (f *fakeDeviceStore) ListDeviceSplits(context.Context, string) ([]storage.DeviceSplit, error) {
	return f.splits, nil
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

func (f *fakeDeviceWatchAPIControl) RetireScope(_ context.Context, scopeID string) (api.NetworkRetireResult, error) {
	return api.NetworkRetireResult{ScopeID: scopeID, Changed: true}, nil
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

func TestControllerAPIHandlerReturnsScopedDeviceDetailWithTemporalEvidence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	oldValidUntil := now.Add(-time.Minute)
	currentValidUntil := now.Add(5 * time.Minute)
	confidence := 0.8
	store := &fakeDeviceStore{detail: storage.DeviceEvidenceDetail{
		Summary: storage.DeviceEvidenceSummary{Device: domain.Device{ID: "device.one", UserLabel: "Speaker", CreatedAt: now.Add(-time.Hour)}, FirstSeen: now.Add(-time.Hour), LastSeen: now.Add(-time.Minute)},
		Evidence: []storage.DeviceIdentityEvidence{
			{OriginalDeviceID: "device.earlier", Kind: domain.ClaimIPv4, Value: "192.168.1.20", ObservedAt: now.Add(-time.Minute), ClaimValidUntil: &currentValidUntil, ClaimConfidence: &confidence, LinkValidUntil: &currentValidUntil, LinkConfidence: &confidence, Authority: domain.LinkInferred, Reason: "device-watch:recent-mac-continuity:ip", SourceSensorID: "sensor.dw", Observation: &storage.DeviceEvidenceObservation{ID: "obs.one", SensorID: "sensor.dw", Kind: "device-neighbor-seen", SourceStream: "device-watch-neighbors", IngestedAt: now.Add(-time.Minute), Attribution: "device-watch:arp-cache"}},
			{Kind: domain.ClaimMAC, Value: "02:00:00:00:00:01", ObservedAt: now.Add(-10 * time.Minute), ClaimValidUntil: &oldValidUntil, LinkValidUntil: &oldValidUntil, Authority: domain.LinkInferred, Reason: "device-watch:new-mac-candidate:mac", SourceSensorID: "sensor.dw"},
		},
	}, dnsHistory: storage.DeviceDNSHistory{Items: []storage.DeviceDNSHistoryItem{{ObservedAt: now.Add(-2 * time.Minute), ClientIP: "192.168.1.20", Name: "private.example", QueryType: "A", Filtering: "blocked"}}, Truncated: true}}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	detail, err := handler.DeviceDetail(context.Background(), api.DeviceDetailParams{DeviceID: "device.one"})
	if err != nil {
		t.Fatal(err)
	}
	if detail.ScopeID != "scope.home" || detail.Device.State != "visible" || len(detail.Evidence) != 2 || !detail.Evidence[0].Current || detail.Evidence[1].Current {
		t.Fatalf("unexpected detail projection: %+v", detail)
	}
	if store.detailQuery.ScopeID != "scope.home" || store.detailQuery.DeviceID != "device.one" || store.detailQuery.Limit != storage.MaxDeviceDetailEvidence {
		t.Fatalf("detail query escaped current scope: %+v", store.detailQuery)
	}
	if !store.detailQuery.AsOf.Equal(now) || len(detail.DNSHistory) != 1 || detail.DNSHistory[0].Name != "private.example" || !detail.DNSHistoryTruncated {
		t.Fatalf("unexpected scoped DNS history: %+v, query %+v", detail.DNSHistory, store.detailQuery)
	}
	if detail.Evidence[0].Source == nil || detail.Evidence[0].Source.Attribution != "device-watch:arp-cache" {
		t.Fatalf("missing source projection: %+v", detail.Evidence[0])
	}
	if detail.Evidence[0].OriginalDeviceID != "device.earlier" || detail.Evidence[1].OriginalDeviceID != "" {
		t.Fatalf("correction provenance lost: %+v", detail.Evidence)
	}
}

func TestControllerAPIHandlerDeviceDetailFailsClosed(t *testing.T) {
	store := &fakeDeviceStore{}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.DeviceDetail(context.Background(), api.DeviceDetailParams{DeviceID: "../bad"}); !errors.Is(err, localapi.ErrInvalidRead) {
		t.Fatalf("invalid device detail error = %v", err)
	}
	store.detailErr = storage.ErrDeviceEvidenceNotFound
	if _, err := handler.DeviceDetail(context.Background(), api.DeviceDetailParams{DeviceID: "device.one"}); !errors.Is(err, localapi.ErrReadTargetNotFound) {
		t.Fatalf("out-of-scope detail error = %v", err)
	}
	store.detailErr = nil
	store.dnsHistoryErr = errors.New("private storage error")
	if _, err := handler.DeviceDetail(context.Background(), api.DeviceDetailParams{DeviceID: "device.one"}); err == nil {
		t.Fatal("DNS history read failure was hidden")
	}
	control.configured = false
	control.scopeID = ""
	if _, err := handler.DeviceDetail(context.Background(), api.DeviceDetailParams{DeviceID: "device.one"}); !errors.Is(err, localapi.ErrReadTargetNotFound) {
		t.Fatalf("unconfigured detail error = %v", err)
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

func TestControllerAPIHandlerScopesDeviceIdentityCorrections(t *testing.T) {
	store := &fakeDeviceStore{mergeChanged: true, merges: []storage.DeviceMerge{{SourceDeviceID: "device.source", TargetDeviceID: "device.target", CreatedAt: time.Unix(10, 0).UTC()}}}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.MergeDevices(context.Background(), api.DeviceMergeParams{SourceDeviceID: "device.source", TargetDeviceID: "device.target"})
	if err != nil || !result.Changed || store.mergeScope != "scope.home" || store.mergeSource != "device.source" || store.mergeTarget != "device.target" {
		t.Fatal("merge scope", result, store, err)
	}
	listed, err := handler.DeviceMerges(context.Background())
	if err != nil || !listed.Configured || listed.ScopeID != "scope.home" || len(listed.Merges) != 1 {
		t.Fatal("merge list", listed, err)
	}
	result, err = handler.UnmergeDevices(context.Background(), api.DeviceUnmergeParams{SourceDeviceID: "device.source"})
	if err != nil || !result.Changed || store.unmergeSource != "device.source" || store.mergeScope != "scope.home" {
		t.Fatal("unmerge scope", result, store, err)
	}
	store.mergeErr = storage.ErrDeviceMergeConflict
	if _, err := handler.MergeDevices(context.Background(), api.DeviceMergeParams{SourceDeviceID: "device.source", TargetDeviceID: "device.target"}); !errors.Is(err, localapi.ErrMutationConflict) {
		t.Fatal("merge conflict", err)
	}
	store.mergeErr = storage.ErrDeviceNotInScope
	if _, err := handler.MergeDevices(context.Background(), api.DeviceMergeParams{SourceDeviceID: "device.source", TargetDeviceID: "device.target"}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatal("cross-scope merge", err)
	}
	store.mergeErr = nil
	for _, params := range []api.DeviceMergeParams{{SourceDeviceID: "../bad", TargetDeviceID: "device.target"}, {SourceDeviceID: "device.same", TargetDeviceID: "device.same"}} {
		if _, err := handler.MergeDevices(context.Background(), params); !errors.Is(err, localapi.ErrInvalidMutation) {
			t.Fatal("invalid merge", params, err)
		}
	}
	control.configured = false
	if _, err := handler.UnmergeDevices(context.Background(), api.DeviceUnmergeParams{SourceDeviceID: "device.source"}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatal("unconfigured unmerge", err)
	}
}

func TestControllerAPIHandlerScopesDeviceSplits(t *testing.T) {
	store := &fakeDeviceStore{splitChanged: true, splits: []storage.DeviceSplit{{ObservationID: "obs.one", SourceDeviceID: "device.source", TargetDeviceID: "device.created"}}}
	control := &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, control)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.SplitDeviceObservation(context.Background(), api.DeviceSplitParams{SourceDeviceID: "device.source", ObservationID: "obs.one"})
	if err != nil || !result.Changed || result.TargetDeviceID != "device.created" || store.splitScope != "scope.home" || store.splitSource != "device.source" || store.splitObs != "obs.one" {
		t.Fatal("split scope", result, store, err)
	}
	listed, err := handler.DeviceSplits(context.Background())
	if err != nil || !listed.Configured || listed.ScopeID != "scope.home" || len(listed.Splits) != 1 {
		t.Fatal("split list", listed, err)
	}
	result, err = handler.UnsplitDeviceObservation(context.Background(), api.DeviceUnsplitParams{ObservationID: "obs.one"})
	if err != nil || !result.Changed || store.splitScope != "scope.home" || store.splitObs != "obs.one" {
		t.Fatal("unsplit scope", result, store, err)
	}
	store.splitErr = storage.ErrDeviceSplitConflict
	if _, err := handler.SplitDeviceObservation(context.Background(), api.DeviceSplitParams{SourceDeviceID: "device.source", ObservationID: "obs.one"}); !errors.Is(err, localapi.ErrMutationConflict) {
		t.Fatal("conflict mapping", err)
	}
	store.splitErr = storage.ErrDeviceNotInScope
	if _, err := handler.SplitDeviceObservation(context.Background(), api.DeviceSplitParams{SourceDeviceID: "device.source", ObservationID: "obs.one"}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatal("scope mapping", err)
	}
	for _, params := range []api.DeviceSplitParams{{SourceDeviceID: "../bad", ObservationID: "obs.one"}, {SourceDeviceID: "device.source", ObservationID: "../bad"}} {
		if _, err := handler.SplitDeviceObservation(context.Background(), params); !errors.Is(err, localapi.ErrInvalidMutation) {
			t.Fatal("invalid params", params, err)
		}
	}
	control.configured = false
	if _, err := handler.SplitDeviceObservation(context.Background(), api.DeviceSplitParams{SourceDeviceID: "device.source", ObservationID: "obs.one"}); !errors.Is(err, localapi.ErrMutationTargetNotFound) {
		t.Fatal("disabled scope", err)
	}
}

func TestControllerAPIHandlerDelegatesDeviceWatchControl(t *testing.T) {
	control := &fakeDeviceWatchAPIControl{
		enableResult:  api.DeviceWatchControlResult{ScopeID: "scope.home", Changed: true, Active: true, State: capability.InstanceState{Desired: capability.DesiredEnabled}},
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

func TestControllerAPIHandlerRejectsChangedReviewedNetworkBeforeEnrollment(t *testing.T) {
	state := devicewatch.InterfaceState{Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.42/24")}}
	store := &fakeDeviceStore{enrollScope: domain.NetworkScope{ID: "scope.generated", Kind: "lan", EnrolledAt: time.Unix(1_800_000_000, 0).UTC()}, enrollChanged: true}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.networkInspector = handlerInspector{states: map[string]devicewatch.InterfaceState{"en0": state}}
	reviewed := &api.NetworkInterface{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.1.0/24"}}
	if _, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "en0", Expected: reviewed}); err != nil {
		t.Fatalf("unchanged reviewed binding: %v", err)
	}

	for name, params := range map[string]api.NetworkEnrollParams{
		"interface index": {InterfaceName: "en0", Expected: &api.NetworkInterface{InterfaceName: "en0", InterfaceIndex: 8, Prefixes: []string{"192.168.1.0/24"}}},
		"prefixes":        {InterfaceName: "en0", Expected: &api.NetworkInterface{InterfaceName: "en0", InterfaceIndex: 7, Prefixes: []string{"192.168.0.0/16"}}},
	} {
		t.Run(name, func(t *testing.T) {
			store.enrollMetadata = nil
			if _, err := handler.EnrollNetwork(context.Background(), params); !errors.Is(err, localapi.ErrMutationPrecondition) {
				t.Fatalf("changed reviewed binding error = %v", err)
			}
			if store.enrollMetadata != nil {
				t.Fatal("changed reviewed binding reached storage")
			}
		})
	}
	store.enrollMetadata = nil
	handler.networkInspector = handlerInspector{states: map[string]devicewatch.InterfaceState{"en0": {
		Name: "en0", Index: 7, Flags: net.FlagUp | net.FlagBroadcast,
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.2.42/24")},
	}}}
	if _, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "en0", Expected: reviewed}); !errors.Is(err, localapi.ErrMutationPrecondition) {
		t.Fatalf("changed OS binding error = %v", err)
	}
	if store.enrollMetadata != nil {
		t.Fatal("changed OS binding reached storage")
	}
	store.enrollMetadata = nil
	if _, err := handler.EnrollNetwork(context.Background(), api.NetworkEnrollParams{InterfaceName: "en0", Expected: &api.NetworkInterface{
		InterfaceName: "en1", InterfaceIndex: 7, Prefixes: []string{"192.168.2.0/24"},
	}}); !errors.Is(err, localapi.ErrInvalidMutation) {
		t.Fatalf("mismatched reviewed interface error = %v", err)
	}
	if store.enrollMetadata != nil {
		t.Fatal("invalid reviewed binding reached storage")
	}
}

func stringPtr(value string) *string {
	return &value
}
