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
	SetDeviceLabel(context.Context, string, string, string) (bool, error)
	ListActiveDeviceWatchScopes(context.Context) ([]domain.NetworkScope, error)
	EnrollDeviceWatchScope(context.Context, json.RawMessage) (domain.NetworkScope, bool, error)
}

type scopeCandidateLister func(context.Context, devicewatch.InterfaceInspector) ([]devicewatch.ScopeBinding, bool, error)

type controllerAPIHandler struct {
	controller            *core.Controller
	store                 controllerStore
	deviceWatchScopeID    string
	deviceWatchConfigured bool
	networkInspector      devicewatch.InterfaceInspector
	listScopeCandidates   scopeCandidateLister
	now                   func() time.Time
}

func newControllerAPIHandler(controller *core.Controller, store controllerStore, scopeID string, configured bool) (*controllerAPIHandler, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller API handler requires controller core")
	}
	if configured && (store == nil || scopeID == "") {
		return nil, fmt.Errorf("configured Device Watch API requires a scope and device store")
	}
	return &controllerAPIHandler{
		controller:            controller,
		store:                 store,
		deviceWatchScopeID:    scopeID,
		deviceWatchConfigured: configured,
		networkInspector:      devicewatch.NewSystemInterfaceInspector(),
		listScopeCandidates:   devicewatch.ListScopeCandidates,
		now:                   time.Now,
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
	result := api.DeviceList{
		Configured: h.deviceWatchConfigured,
		AsOf:       asOf,
		Devices:    []api.DevicePresence{},
	}
	if !h.deviceWatchConfigured {
		return result, nil
	}

	presence, err := devicewatch.ListPresence(ctx, h.store, h.deviceWatchScopeID, asOf, "", storage.MaxQueryLimit)
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

func (h *controllerAPIHandler) LabelDevice(ctx context.Context, params api.DeviceLabelParams) (api.DeviceLabelResult, error) {
	if !h.deviceWatchConfigured || h.store == nil || h.deviceWatchScopeID == "" {
		return api.DeviceLabelResult{}, localapi.ErrMutationTargetNotFound
	}
	if params.Label == nil || !deviceIDPattern.MatchString(params.DeviceID) || storage.ValidateDeviceLabel(*params.Label) != nil {
		return api.DeviceLabelResult{}, localapi.ErrInvalidMutation
	}
	label := *params.Label

	changed, err := h.store.SetDeviceLabel(ctx, h.deviceWatchScopeID, params.DeviceID, label)
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
