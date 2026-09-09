package main

import (
	"context"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type controllerAPIHandler struct {
	controller            *core.Controller
	presenceReader         devicewatch.DeviceEvidenceReader
	deviceWatchScopeID     string
	deviceWatchConfigured  bool
	now                    func() time.Time
}

func newControllerAPIHandler(controller *core.Controller, reader devicewatch.DeviceEvidenceReader, scopeID string, configured bool) (*controllerAPIHandler, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller API handler requires controller core")
	}
	if configured && (reader == nil || scopeID == "") {
		return nil, fmt.Errorf("configured Device Watch API requires a scope and presence reader")
	}
	return &controllerAPIHandler{
		controller:           controller,
		presenceReader:        reader,
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

	presence, err := devicewatch.ListPresence(ctx, h.presenceReader, h.deviceWatchScopeID, asOf, "", storage.MaxQueryLimit)
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
