package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

var deviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

type controllerDeviceStore interface {
	devicewatch.DeviceEvidenceReader
	SetDeviceLabel(context.Context, string, string, string) (bool, error)
}

type controllerAPIHandler struct {
	controller            *core.Controller
	deviceStore           controllerDeviceStore
	deviceWatchScopeID    string
	deviceWatchConfigured bool
	now                   func() time.Time
}

func newControllerAPIHandler(controller *core.Controller, store controllerDeviceStore, scopeID string, configured bool) (*controllerAPIHandler, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller API handler requires controller core")
	}
	if configured && (store == nil || scopeID == "") {
		return nil, fmt.Errorf("configured Device Watch API requires a scope and device store")
	}
	return &controllerAPIHandler{
		controller:            controller,
		deviceStore:           store,
		deviceWatchScopeID:    scopeID,
		deviceWatchConfigured: configured,
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

	presence, err := devicewatch.ListPresence(ctx, h.deviceStore, h.deviceWatchScopeID, asOf, "", storage.MaxQueryLimit)
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
	if !h.deviceWatchConfigured || h.deviceStore == nil || h.deviceWatchScopeID == "" {
		return api.DeviceLabelResult{}, localapi.ErrMutationTargetNotFound
	}
	if !deviceIDPattern.MatchString(params.DeviceID) || storage.ValidateDeviceLabel(params.Label) != nil {
		return api.DeviceLabelResult{}, localapi.ErrInvalidMutation
	}

	changed, err := h.deviceStore.SetDeviceLabel(ctx, h.deviceWatchScopeID, params.DeviceID, params.Label)
	if errors.Is(err, storage.ErrDeviceNotInScope) {
		return api.DeviceLabelResult{}, localapi.ErrMutationTargetNotFound
	}
	if err != nil {
		return api.DeviceLabelResult{}, err
	}
	return api.DeviceLabelResult{
		DeviceID:  params.DeviceID,
		UserLabel: params.Label,
		Changed:   changed,
	}, nil
}
