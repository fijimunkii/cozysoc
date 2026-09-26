package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverroute"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

var deviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

type controllerStore interface {
	devicewatch.DeviceEvidenceReader
	devicewatch.CoverageSampleReader
	GetDeviceDetailSnapshot(context.Context, storage.DeviceEvidenceDetailQuery) (storage.DeviceDetailSnapshot, error)
	ListDeviceActivity(context.Context, storage.DeviceActivityQuery) (storage.DeviceActivityPage, error)
	SetDeviceLabel(context.Context, string, string, string) (bool, error)
	MergeDevices(context.Context, string, string, string) (bool, error)
	UnmergeDevices(context.Context, string, string) (bool, error)
	ListDeviceMerges(context.Context, string) ([]storage.DeviceMerge, error)
	SplitDeviceObservation(context.Context, string, string, string, string) (string, bool, error)
	UndoDeviceSplitObservation(context.Context, string, string) (bool, error)
	ListDeviceSplits(context.Context, string) ([]storage.DeviceSplit, error)
	ListActiveDeviceWatchScopes(context.Context) ([]domain.NetworkScope, error)
	EnrollDeviceWatchScope(context.Context, json.RawMessage) (domain.NetworkScope, bool, error)
}

type deviceWatchAPIControl interface {
	Current() (string, bool, error)
	Enable(context.Context) (api.DeviceWatchControlResult, error)
	Disable(context.Context) (api.DeviceWatchControlResult, error)
	RetireScope(context.Context, string) (api.NetworkRetireResult, error)
}

type deviceWatchOperationalControl interface {
	OperationalHealth(context.Context, time.Time) (devicewatch.OperationalHealth, error)
}

type storageOverviewReader interface {
	Health(context.Context) (storage.Health, error)
	RetentionDurations() map[domain.RetentionClass]time.Duration
}

type scopeCandidateLister func(context.Context, devicewatch.InterfaceInspector) ([]devicewatch.ScopeBinding, bool, error)

type adguardConnectionControl interface {
	Connect(context.Context, string, string, secretstore.Secret) (adguard.Connection, error)
	Current(context.Context) (adguard.Connection, error)
	Disconnect(context.Context) error
}

type adguardCollectionControl interface {
	Collect(context.Context, string) (adguard.CollectionResult, error)
}

type controllerAPIHandler struct {
	controller             *core.Controller
	gatewayRuns            gatewayRunLifecycle
	resolverRuns           resolverRunLifecycle
	httpsRuns              httpsRunLifecycle
	httpsRouteInspector    httpsroute.Inspector
	resolverRouteInspector resolverroute.Inspector
	httpsChecksEnabled     bool
	resolverChecksEnabled  bool
	gatewayChecksEnabled   bool // Immutable after server startup; experimental native opt-in only.
	store                  controllerStore
	storageOverview        storageOverviewReader
	deviceWatch            deviceWatchAPIControl
	gatewayRouteInspector  gatewayroute.Inspector
	networkInspector       devicewatch.InterfaceInspector
	listScopeCandidates    scopeCandidateLister
	adguardConnections     adguardConnectionControl
	adguardCollector       adguardCollectionControl
	now                    func() time.Time
}

func newControllerAPIHandler(controller *core.Controller, store controllerStore, deviceWatch deviceWatchAPIControl) (*controllerAPIHandler, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller API handler requires controller core")
	}
	if store == nil || deviceWatch == nil {
		return nil, fmt.Errorf("controller API handler requires storage and Device Watch control")
	}
	return &controllerAPIHandler{
		controller:             controller,
		store:                  store,
		deviceWatch:            deviceWatch,
		gatewayRouteInspector:  gatewayroute.NewInspector(),
		resolverRouteInspector: resolverroute.NewInspector(),
		httpsRouteInspector:    httpsroute.NewInspector(),
		networkInspector:       devicewatch.NewSystemInterfaceInspector(),
		listScopeCandidates:    devicewatch.ListScopeCandidates,
		now:                    time.Now,
	}, nil
}

func (h *controllerAPIHandler) Status() api.Status {
	return h.controller.Status()
}

func (h *controllerAPIHandler) Health() api.Health {
	return h.controller.Health()
}

func (h *controllerAPIHandler) StorageOverview(ctx context.Context) (api.StorageOverview, error) {
	if h.storageOverview == nil {
		return api.StorageOverview{}, fmt.Errorf("storage overview is unavailable")
	}
	health, err := h.storageOverview.Health(ctx)
	if err != nil {
		return api.StorageOverview{}, err
	}
	durations := h.storageOverview.RetentionDurations()
	retention := make([]api.StorageRetention, 0, 4)
	for _, class := range []domain.RetentionClass{domain.RetentionEphemeral, domain.RetentionShort, domain.RetentionStandard, domain.RetentionAudit} {
		duration := durations[class]
		if duration <= 0 {
			return api.StorageOverview{}, fmt.Errorf("storage retention policy is incomplete")
		}
		retention = append(retention, api.StorageRetention{Class: string(class), DurationSeconds: int64(duration.Seconds())})
	}
	return api.StorageOverview{
		AsOf: h.now().UTC(), QuotaState: string(health.QuotaState), DatabaseBytes: health.DatabaseBytes,
		UsedBytes: health.UsedBytes, ReusableBytes: health.ReusableBytes, MaxBytes: health.MaxBytes,
		FilesystemState: string(health.FilesystemState), FilesystemSupported: health.FilesystemSupported,
		FilesystemTotalBytes: health.FilesystemTotalBytes, FilesystemAvailableBytes: health.FilesystemAvailableBytes,
		Retention: retention,
	}, nil
}

func (h *controllerAPIHandler) Capabilities() api.CapabilityList {
	return h.controller.Capabilities()
}

func (h *controllerAPIHandler) Devices(ctx context.Context) (api.DeviceList, error) {
	asOf := h.now().UTC()
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceList{}, err
	}
	result := api.DeviceList{
		Configured: configured,
		AsOf:       asOf,
		Devices:    []api.DevicePresence{},
	}
	if !configured {
		return result, nil
	}

	presence, err := devicewatch.ListPresence(ctx, h.store, scopeID, asOf, "", storage.MaxQueryLimit)
	if err != nil {
		return api.DeviceList{}, err
	}
	result.ScopeID = presence.ScopeID
	result.Truncated = presence.NextID != ""
	result.Devices = make([]api.DevicePresence, 0, len(presence.Devices))
	for _, device := range presence.Devices {
		result.Devices = append(result.Devices, api.DevicePresence{
			ID:        device.ID,
			UserLabel: device.UserLabel,
			FirstSeen: device.FirstSeen,
			LastSeen:  device.LastSeen,
			State:     string(device.State),
		})
	}
	return result, nil
}

func (h *controllerAPIHandler) DeviceActivity(ctx context.Context) (api.DeviceActivityList, error) {
	asOf := h.now().UTC()
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceActivityList{}, err
	}
	result := api.DeviceActivityList{
		Configured: configured,
		Since:      asOf.Add(-storage.DeviceActivityWindow),
		AsOf:       asOf,
		Items:      []api.DeviceActivityItem{},
	}
	if !configured || scopeID == "" {
		return result, nil
	}
	page, err := h.store.ListDeviceActivity(ctx, storage.DeviceActivityQuery{ScopeID: scopeID, AsOf: asOf, Limit: storage.MaxDeviceActivityItems})
	if err != nil {
		return api.DeviceActivityList{}, err
	}
	result.ScopeID = scopeID
	result.Since = page.Since
	result.Truncated = page.Truncated
	result.Items = make([]api.DeviceActivityItem, 0, len(page.Items))
	for _, item := range page.Items {
		result.Items = append(result.Items, api.DeviceActivityItem{
			ID: item.ID, Kind: string(item.Kind), At: item.At, DeviceID: item.DeviceID, UserLabel: item.UserLabel,
			AddressFamily: item.AddressFamily, Address: item.Address, PreviousAddress: item.PreviousAddress,
			HardwareAddress: item.HardwareAddress,
			Source: api.DeviceEvidenceSource{ObservationID: item.Source.ObservationID, SensorID: item.Source.SensorID,
				Kind: item.Source.Kind, SourceStream: item.Source.SourceStream, IngestedAt: item.Source.IngestedAt, Attribution: item.Source.Attribution},
		})
	}
	return result, nil
}

func (h *controllerAPIHandler) DeviceDetail(ctx context.Context, params api.DeviceDetailParams) (api.DeviceDetail, error) {
	if !deviceIDPattern.MatchString(params.DeviceID) {
		return api.DeviceDetail{}, localapi.ErrInvalidRead
	}
	asOf := h.now().UTC()
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceDetail{}, err
	}
	if !configured || scopeID == "" {
		return api.DeviceDetail{}, localapi.ErrReadTargetNotFound
	}
	snapshot, err := h.store.GetDeviceDetailSnapshot(ctx, storage.DeviceEvidenceDetailQuery{ScopeID: scopeID, DeviceID: params.DeviceID, AsOf: asOf, Limit: storage.MaxDeviceDetailEvidence})
	if errors.Is(err, storage.ErrDeviceEvidenceNotFound) {
		return api.DeviceDetail{}, localapi.ErrReadTargetNotFound
	}
	if err != nil {
		return api.DeviceDetail{}, err
	}
	detail, dnsHistory := snapshot.Detail, snapshot.DNSHistory
	presence := devicewatch.PresenceFromEvidence(detail.Summary, asOf)
	result := api.DeviceDetail{
		ScopeID: scopeID,
		AsOf:    asOf,
		Device: api.DevicePresence{
			ID: presence.ID, UserLabel: presence.UserLabel, FirstSeen: presence.FirstSeen,
			LastSeen: presence.LastSeen, State: string(presence.State),
		},
		Evidence:            make([]api.DeviceIdentityEvidence, 0, len(detail.Evidence)),
		Truncated:           detail.Truncated,
		DNSHistory:          make([]api.DeviceDNSHistoryItem, 0, len(dnsHistory.Items)),
		DNSHistoryTruncated: dnsHistory.Truncated,
	}
	for _, item := range dnsHistory.Items {
		result.DNSHistory = append(result.DNSHistory, api.DeviceDNSHistoryItem{ObservedAt: item.ObservedAt, ClientIP: item.ClientIP, Name: item.Name, QueryType: item.QueryType, Filtering: item.Filtering})
	}
	for _, evidence := range detail.Evidence {
		current := (evidence.ClaimValidUntil == nil || evidence.ClaimValidUntil.After(asOf)) &&
			(evidence.LinkValidUntil == nil || evidence.LinkValidUntil.After(asOf))
		item := api.DeviceIdentityEvidence{
			OriginalDeviceID: evidence.OriginalDeviceID,
			Kind:             string(evidence.Kind), Value: evidence.Value, ObservedAt: evidence.ObservedAt,
			ValidUntil: evidence.ClaimValidUntil, LinkValidUntil: evidence.LinkValidUntil, Current: current, ClaimConfidence: evidence.ClaimConfidence,
			LinkConfidence: evidence.LinkConfidence, Authority: string(evidence.Authority), Reason: evidence.Reason,
			SourceSensorID: evidence.SourceSensorID,
		}
		if evidence.Observation != nil {
			item.Source = &api.DeviceEvidenceSource{
				ObservationID: evidence.Observation.ID, SensorID: evidence.Observation.SensorID,
				Kind: evidence.Observation.Kind, SourceStream: evidence.Observation.SourceStream,
				IngestedAt: evidence.Observation.IngestedAt, Attribution: evidence.Observation.Attribution,
			}
		}
		result.Evidence = append(result.Evidence, item)
	}
	return result, nil
}

func (h *controllerAPIHandler) DeviceWatchCoverage(ctx context.Context) (api.DeviceWatchCoverage, error) {
	asOf := h.now().UTC()
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceWatchCoverage{}, err
	}
	if !configured {
		contract := devicewatch.UnconfiguredCoverageContract()
		projected := projectCoverageReport(contract)
		return api.DeviceWatchCoverage{
			Configured: false,
			AsOf:       asOf,
			State:      projected.State,
			Reason:     projected.Reason,
			Coverage:   &projected,
			Sources:    []api.DeviceWatchCoverageSource{},
			BlindSpots: []api.DeviceWatchCoverageBlindSpot{},
			NextStep:   projected.NextStep,
		}, nil
	}

	report, err := devicewatch.CurrentCoverage(ctx, h.store, scopeID, asOf)
	if err != nil {
		return api.DeviceWatchCoverage{}, err
	}
	operationalControl, ok := h.deviceWatch.(deviceWatchOperationalControl)
	if !ok {
		return api.DeviceWatchCoverage{}, fmt.Errorf("Device Watch operational health is unavailable")
	}
	operational, err := operationalControl.OperationalHealth(ctx, asOf)
	if err != nil {
		return api.DeviceWatchCoverage{}, err
	}
	contract, err := devicewatch.CoverageContract(report, operational)
	if err != nil {
		return api.DeviceWatchCoverage{}, err
	}
	projected := projectCoverageReport(contract)
	result := api.DeviceWatchCoverage{
		Configured:    projected.Configured,
		ScopeID:       scopeID,
		AsOf:          asOf,
		State:         projected.State,
		Reason:        projected.Reason,
		Coverage:      &projected,
		SensorID:      report.SensorID,
		InterfaceName: report.InterfaceName,
		Sources:       make([]api.DeviceWatchCoverageSource, 0, len(report.Sources)),
		Operational:   projectDeviceWatchOperational(operational),
		BlindSpots:    make([]api.DeviceWatchCoverageBlindSpot, 0, len(report.BlindSpots)),
		NextStep:      projected.NextStep,
	}
	if report.HasEvidence {
		evidenceAt := report.EvidenceAt
		freshUntil := report.FreshUntil
		neighbors := report.NeighborsInScope
		result.EvidenceAt = &evidenceAt
		result.FreshUntil = &freshUntil
		result.NeighborsInScope = &neighbors
	}
	for _, source := range report.Sources {
		result.Sources = append(result.Sources, api.DeviceWatchCoverageSource{
			ID:                    source.ID,
			AddressFamily:         source.AddressFamily,
			State:                 string(source.State),
			Reported:              source.Observed,
			AvailableAtLastSample: source.AvailableAtLastSample,
			NextStep:              source.NextStep,
		})
	}
	for _, blindSpot := range report.BlindSpots {
		result.BlindSpots = append(result.BlindSpots, api.DeviceWatchCoverageBlindSpot{
			ID:       blindSpot.ID,
			Summary:  blindSpot.Summary,
			Detail:   blindSpot.Detail,
			NextStep: blindSpot.NextStep,
		})
	}
	return result, nil
}

func (h *controllerAPIHandler) LabelDevice(ctx context.Context, params api.DeviceLabelParams) (api.DeviceLabelResult, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceLabelResult{}, err
	}
	if !configured || scopeID == "" {
		return api.DeviceLabelResult{}, localapi.ErrMutationTargetNotFound
	}
	if params.Label == nil || !deviceIDPattern.MatchString(params.DeviceID) || storage.ValidateDeviceLabel(*params.Label) != nil {
		return api.DeviceLabelResult{}, localapi.ErrInvalidMutation
	}
	label := *params.Label

	changed, err := h.store.SetDeviceLabel(ctx, scopeID, params.DeviceID, label)
	if errors.Is(err, storage.ErrDeviceNotInScope) {
		return api.DeviceLabelResult{}, localapi.ErrMutationTargetNotFound
	}
	if err != nil {
		return api.DeviceLabelResult{}, err
	}
	return api.DeviceLabelResult{
		DeviceID:  params.DeviceID,
		UserLabel: label,
		Changed:   changed,
	}, nil
}

func (h *controllerAPIHandler) MergeDevices(ctx context.Context, params api.DeviceMergeParams) (api.DeviceIdentityCorrectionResult, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceIdentityCorrectionResult{}, err
	}
	if !configured || scopeID == "" {
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrMutationTargetNotFound
	}
	if !deviceIDPattern.MatchString(params.SourceDeviceID) || !deviceIDPattern.MatchString(params.TargetDeviceID) || params.SourceDeviceID == params.TargetDeviceID {
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrInvalidMutation
	}
	changed, err := h.store.MergeDevices(ctx, scopeID, params.SourceDeviceID, params.TargetDeviceID)
	switch {
	case errors.Is(err, storage.ErrDeviceNotInScope):
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrMutationTargetNotFound
	case errors.Is(err, storage.ErrDeviceMergeConflict), errors.Is(err, storage.ErrDeviceMergeLimit):
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrMutationConflict
	case err != nil:
		return api.DeviceIdentityCorrectionResult{}, err
	}
	return api.DeviceIdentityCorrectionResult{SourceDeviceID: params.SourceDeviceID, TargetDeviceID: params.TargetDeviceID, Changed: changed}, nil
}

func (h *controllerAPIHandler) UnmergeDevices(ctx context.Context, params api.DeviceUnmergeParams) (api.DeviceIdentityCorrectionResult, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceIdentityCorrectionResult{}, err
	}
	if !configured || scopeID == "" {
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrMutationTargetNotFound
	}
	if !deviceIDPattern.MatchString(params.SourceDeviceID) {
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrInvalidMutation
	}
	changed, err := h.store.UnmergeDevices(ctx, scopeID, params.SourceDeviceID)
	if errors.Is(err, storage.ErrDeviceNotInScope) {
		return api.DeviceIdentityCorrectionResult{}, localapi.ErrMutationTargetNotFound
	}
	if err != nil {
		return api.DeviceIdentityCorrectionResult{}, err
	}
	return api.DeviceIdentityCorrectionResult{SourceDeviceID: params.SourceDeviceID, Changed: changed}, nil
}

func (h *controllerAPIHandler) DeviceMerges(ctx context.Context) (api.DeviceMergeList, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceMergeList{}, err
	}
	result := api.DeviceMergeList{Configured: configured, Merges: []api.DeviceMerge{}}
	if !configured || scopeID == "" {
		return result, nil
	}
	result.ScopeID = scopeID
	items, err := h.store.ListDeviceMerges(ctx, scopeID)
	if err != nil {
		return api.DeviceMergeList{}, err
	}
	for _, item := range items {
		result.Merges = append(result.Merges, api.DeviceMerge{
			SourceDeviceID: item.SourceDeviceID, TargetDeviceID: item.TargetDeviceID, CreatedAt: item.CreatedAt,
		})
	}
	return result, nil
}

func (h *controllerAPIHandler) SplitDeviceObservation(ctx context.Context, params api.DeviceSplitParams) (api.DeviceSplitResult, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceSplitResult{}, err
	}
	if !configured || scopeID == "" {
		return api.DeviceSplitResult{}, localapi.ErrMutationTargetNotFound
	}
	if !deviceIDPattern.MatchString(params.SourceDeviceID) || !deviceIDPattern.MatchString(params.ObservationID) || params.TargetDeviceID != "" && !deviceIDPattern.MatchString(params.TargetDeviceID) {
		return api.DeviceSplitResult{}, localapi.ErrInvalidMutation
	}
	targetID, changed, err := h.store.SplitDeviceObservation(ctx, scopeID, params.SourceDeviceID, params.ObservationID, params.TargetDeviceID)
	switch {
	case errors.Is(err, storage.ErrDeviceNotInScope):
		return api.DeviceSplitResult{}, localapi.ErrMutationTargetNotFound
	case errors.Is(err, storage.ErrDeviceSplitConflict), errors.Is(err, storage.ErrDeviceSplitLimit), errors.Is(err, storage.ErrDeviceMergeConflict):
		return api.DeviceSplitResult{}, localapi.ErrMutationConflict
	case err != nil:
		return api.DeviceSplitResult{}, err
	}
	return api.DeviceSplitResult{ObservationID: params.ObservationID, SourceDeviceID: params.SourceDeviceID, TargetDeviceID: targetID, Changed: changed}, nil
}

func (h *controllerAPIHandler) UnsplitDeviceObservation(ctx context.Context, params api.DeviceUnsplitParams) (api.DeviceSplitResult, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceSplitResult{}, err
	}
	if !configured || scopeID == "" {
		return api.DeviceSplitResult{}, localapi.ErrMutationTargetNotFound
	}
	if !deviceIDPattern.MatchString(params.ObservationID) {
		return api.DeviceSplitResult{}, localapi.ErrInvalidMutation
	}
	changed, err := h.store.UndoDeviceSplitObservation(ctx, scopeID, params.ObservationID)
	if err != nil {
		return api.DeviceSplitResult{}, err
	}
	return api.DeviceSplitResult{ObservationID: params.ObservationID, Changed: changed}, nil
}

func (h *controllerAPIHandler) DeviceSplits(ctx context.Context) (api.DeviceSplitList, error) {
	scopeID, configured, err := h.deviceWatch.Current()
	if err != nil {
		return api.DeviceSplitList{}, err
	}
	result := api.DeviceSplitList{Configured: configured, Splits: []api.DeviceSplit{}}
	if !configured || scopeID == "" {
		return result, nil
	}
	result.ScopeID = scopeID
	items, err := h.store.ListDeviceSplits(ctx, scopeID)
	if err != nil {
		return api.DeviceSplitList{}, err
	}
	for _, item := range items {
		result.Splits = append(result.Splits, api.DeviceSplit{ObservationID: item.ObservationID, SourceDeviceID: item.SourceDeviceID, TargetDeviceID: item.TargetDeviceID, CreatedAt: item.CreatedAt})
	}
	return result, nil
}

func (h *controllerAPIHandler) EnableDeviceWatch(ctx context.Context) (api.DeviceWatchControlResult, error) {
	return h.deviceWatch.Enable(ctx)
}

func (h *controllerAPIHandler) DisableDeviceWatch(ctx context.Context) (api.DeviceWatchControlResult, error) {
	return h.deviceWatch.Disable(ctx)
}

func (h *controllerAPIHandler) Networks(ctx context.Context) (api.NetworkList, error) {
	if h.store == nil || h.networkInspector == nil || h.listScopeCandidates == nil {
		return api.NetworkList{}, fmt.Errorf("network enrollment service is unavailable")
	}
	bindings, truncated, err := h.listScopeCandidates(ctx, h.networkInspector)
	if err != nil {
		return api.NetworkList{}, err
	}
	result := api.NetworkList{
		Candidates:          make([]api.NetworkInterface, 0, len(bindings)),
		CandidatesTruncated: truncated,
	}
	for _, binding := range bindings {
		result.Candidates = append(result.Candidates, networkInterface(binding))
	}

	scopes, err := h.store.ListActiveDeviceWatchScopes(ctx)
	if err != nil {
		return api.NetworkList{}, err
	}
	if len(scopes) > 1 {
		return api.NetworkList{}, fmt.Errorf("multiple active Device Watch scopes violate the v0.1 enrollment invariant")
	}
	if len(scopes) == 1 {
		binding, err := devicewatch.ParseScopeBinding(scopes[0])
		if err != nil {
			return api.NetworkList{}, err
		}
		result.Enrolled = &api.EnrolledNetwork{
			ScopeID:    scopes[0].ID,
			EnrolledAt: scopes[0].EnrolledAt,
			Interface:  networkInterface(binding),
		}
	}
	return result, nil
}

func (h *controllerAPIHandler) EnrollNetwork(ctx context.Context, params api.NetworkEnrollParams) (api.NetworkEnrollResult, error) {
	if h.store == nil || h.networkInspector == nil {
		return api.NetworkEnrollResult{}, fmt.Errorf("network enrollment service is unavailable")
	}
	if devicewatch.ValidateEnrollmentInterfaceName(params.InterfaceName) != nil {
		return api.NetworkEnrollResult{}, localapi.ErrInvalidMutation
	}
	binding, err := devicewatch.CaptureScopeBinding(ctx, h.networkInspector, params.InterfaceName)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return api.NetworkEnrollResult{}, err
		}
		return api.NetworkEnrollResult{}, localapi.ErrMutationPrecondition
	}
	if params.Expected != nil {
		expected := devicewatch.ScopeBinding{InterfaceName: params.Expected.InterfaceName, InterfaceIndex: params.Expected.InterfaceIndex, Prefixes: params.Expected.Prefixes}
		if expected.InterfaceName != params.InterfaceName || devicewatch.ValidateScopeBinding(expected) != nil {
			return api.NetworkEnrollResult{}, localapi.ErrInvalidMutation
		}
		prefixes := slices.Clone(expected.Prefixes)
		slices.Sort(prefixes)
		if binding.InterfaceName != expected.InterfaceName || binding.InterfaceIndex != expected.InterfaceIndex || !slices.Equal(binding.Prefixes, prefixes) {
			return api.NetworkEnrollResult{}, localapi.ErrMutationPrecondition
		}
	}
	metadata, err := devicewatch.EncodeScopeMetadata(binding)
	if err != nil {
		return api.NetworkEnrollResult{}, err
	}
	scope, changed, err := h.store.EnrollDeviceWatchScope(ctx, metadata)
	if errors.Is(err, storage.ErrActiveDeviceWatchScopeExists) {
		return api.NetworkEnrollResult{}, localapi.ErrMutationConflict
	}
	if err != nil {
		return api.NetworkEnrollResult{}, err
	}
	return api.NetworkEnrollResult{
		ScopeID:    scope.ID,
		EnrolledAt: scope.EnrolledAt,
		Interface:  networkInterface(binding),
		Changed:    changed,
	}, nil
}

func (h *controllerAPIHandler) RetireNetwork(ctx context.Context, params api.NetworkRetireParams) (api.NetworkRetireResult, error) {
	if !deviceIDPattern.MatchString(params.ScopeID) {
		return api.NetworkRetireResult{}, localapi.ErrInvalidMutation
	}
	return h.deviceWatch.RetireScope(ctx, params.ScopeID)
}

func networkInterface(binding devicewatch.ScopeBinding) api.NetworkInterface {
	return api.NetworkInterface{
		InterfaceName:  binding.InterfaceName,
		InterfaceIndex: binding.InterfaceIndex,
		Prefixes:       append([]string(nil), binding.Prefixes...),
	}
}
