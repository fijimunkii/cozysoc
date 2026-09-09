package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

var deviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

type controllerStore interface {
	devicewatch.DeviceEvidenceReader
	devicewatch.CoverageSampleReader
	SetDeviceLabel(context.Context, string, string, string) (bool, error)
	ListActiveDeviceWatchScopes(context.Context) ([]domain.NetworkScope, error)
	EnrollDeviceWatchScope(context.Context, json.RawMessage) (domain.NetworkScope, bool, error)
}

type deviceWatchAPIControl interface {
	Current() (string, bool, error)
	Enable(context.Context) (api.DeviceWatchControlResult, error)
	Disable(context.Context) (api.DeviceWatchControlResult, error)
}

type deviceWatchOperationalControl interface {
	OperationalHealth(context.Context, time.Time) (devicewatch.OperationalHealth, error)
}

type scopeCandidateLister func(context.Context, devicewatch.InterfaceInspector) ([]devicewatch.ScopeBinding, bool, error)

type controllerAPIHandler struct {
	controller          *core.Controller
	store               controllerStore
	deviceWatch         deviceWatchAPIControl
	networkInspector    devicewatch.InterfaceInspector
	listScopeCandidates scopeCandidateLister
	now                 func() time.Time
}

func newControllerAPIHandler(controller *core.Controller, store controllerStore, deviceWatch deviceWatchAPIControl) (*controllerAPIHandler, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller API handler requires controller core")
	}
	if store == nil || deviceWatch == nil {
		return nil, fmt.Errorf("controller API handler requires storage and Device Watch control")
	}
	return &controllerAPIHandler{
		controller:          controller,
		store:               store,
		deviceWatch:         deviceWatch,
		networkInspector:    devicewatch.NewSystemInterfaceInspector(),
		listScopeCandidates: devicewatch.ListScopeCandidates,
		now:                 time.Now,
	}, nil
}

func (h *controllerAPIHandler) Status() api.Status {
	return h.controller.Status()
}

func (h *controllerAPIHandler) Health() api.Health {
	return h.controller.Health()
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

func networkInterface(binding devicewatch.ScopeBinding) api.NetworkInterface {
	return api.NetworkInterface{
		InterfaceName:  binding.InterfaceName,
		InterfaceIndex: binding.InterfaceIndex,
		Prefixes:       append([]string(nil), binding.Prefixes...),
	}
}
