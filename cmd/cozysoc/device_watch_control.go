package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/config"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type deviceWatchActivity interface {
	Active() bool
	Close(context.Context) error
}

type deviceWatchControl struct {
	mu        sync.Mutex
	configs   *config.Manager
	lifecycle *capability.LifecycleEngine
	activity  deviceWatchActivity
	store     *storage.Store
	logger    *slog.Logger
}

func newDeviceWatchControl(configs *config.Manager, lifecycle *capability.LifecycleEngine, activity deviceWatchActivity, store *storage.Store, logger *slog.Logger) (*deviceWatchControl, error) {
	if configs == nil || lifecycle == nil || activity == nil || store == nil {
		return nil, fmt.Errorf("Device Watch control dependencies are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &deviceWatchControl{configs: configs, lifecycle: lifecycle, activity: activity, store: store, logger: logger}, nil
}

func (c *deviceWatchControl) Current() (string, bool, error) {
	configured, ok := c.configs.Capability(devicewatch.CapabilityID)
	if !ok || configured.Desired != capability.DesiredEnabled {
		return "", false, nil
	}
	scopeID, err := configuredScopeID(configured)
	if err != nil {
		return "", false, err
	}
	return scopeID, true, nil
}

func (c *deviceWatchControl) Enable(ctx context.Context) (api.DeviceWatchControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if current, ok := c.configs.Capability(devicewatch.CapabilityID); ok && current.Desired == capability.DesiredEnabled {
		scopeID, err := configuredScopeID(current)
		if err != nil {
			return api.DeviceWatchControlResult{}, err
		}
		result, err := c.lifecycle.Run(ctx, devicewatch.CapabilityID, capability.ActionEnable)
		if err != nil {
			c.emergencyStop()
			return api.DeviceWatchControlResult{}, mapDeviceWatchLifecycleError(err)
		}
		return c.controlResult(scopeID, result.Changed), nil
	}

	scopes, err := c.store.ListActiveDeviceWatchScopes(ctx)
	if err != nil {
		return api.DeviceWatchControlResult{}, err
	}
	if len(scopes) == 0 {
		return api.DeviceWatchControlResult{}, localapi.ErrMutationPrecondition
	}
	if len(scopes) != 1 {
		return api.DeviceWatchControlResult{}, localapi.ErrMutationConflict
	}
	scopeID := scopes[0].ID
	rawScopeID, err := json.Marshal(scopeID)
	if err != nil {
		return api.DeviceWatchControlResult{}, err
	}
	proposed := capability.Configuration{
		ID:        devicewatch.CapabilityID,
		Ownership: capability.OwnershipBuiltin,
		Desired:   capability.DesiredEnabled,
		Values:    map[string]json.RawMessage{"network_scope_id": rawScopeID},
	}
	if _, err := c.lifecycle.PreflightConfiguration(ctx, proposed, capability.ActionEnable); err != nil {
		return api.DeviceWatchControlResult{}, mapDeviceWatchLifecycleError(err)
	}
	if err := c.store.InsertCapabilityIntentAudit(ctx, devicewatch.CapabilityID, capability.DesiredEnabled, "requested", scopeID, ""); err != nil {
		return api.DeviceWatchControlResult{}, err
	}

	previous, configChanged, err := c.configs.ReplaceCapability(devicewatch.CapabilityID, &proposed)
	if err != nil {
		c.recordOutcome(capability.DesiredEnabled, scopeID, "failed", "config-write")
		return api.DeviceWatchControlResult{}, err
	}
	if err := c.lifecycle.ApplyConfiguration(proposed); err != nil {
		rollbackErr := c.restorePreviousConfiguration(previous)
		c.recordOutcome(capability.DesiredEnabled, scopeID, "failed", "config-apply")
		if rollbackErr != nil {
			return api.DeviceWatchControlResult{}, fmt.Errorf("apply Device Watch configuration: %v; rollback: %w", err, rollbackErr)
		}
		return api.DeviceWatchControlResult{}, err
	}

	lifecycleResult, err := c.lifecycle.Run(ctx, devicewatch.CapabilityID, capability.ActionEnable)
	if err != nil {
		c.emergencyStop()
		rollbackErr := c.restorePreviousConfiguration(previous)
		c.recordOutcome(capability.DesiredEnabled, scopeID, "failed", "runtime-enable")
		if rollbackErr != nil {
			return api.DeviceWatchControlResult{}, fmt.Errorf("enable Device Watch: %v; rollback: %w", err, rollbackErr)
		}
		return api.DeviceWatchControlResult{}, mapDeviceWatchLifecycleError(err)
	}
	c.recordOutcome(capability.DesiredEnabled, scopeID, "applied", "")
	return c.controlResult(scopeID, configChanged || lifecycleResult.Changed), nil
}

func (c *deviceWatchControl) Disable(ctx context.Context) (api.DeviceWatchControlResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current, configured := c.configs.Capability(devicewatch.CapabilityID)
	if !configured || current.Desired == capability.DesiredDisabled {
		if !c.activity.Active() {
			return c.controlResult(configuredScopeIDBestEffort(current), false), nil
		}
		if err := c.store.InsertCapabilityIntentAudit(ctx, devicewatch.CapabilityID, capability.DesiredDisabled, "requested", configuredScopeIDBestEffort(current), ""); err != nil {
			return api.DeviceWatchControlResult{}, err
		}
		result, err := c.lifecycle.Run(ctx, devicewatch.CapabilityID, capability.ActionDisable)
		if err != nil {
			c.emergencyStop()
			c.recordOutcome(capability.DesiredDisabled, configuredScopeIDBestEffort(current), "failed", "runtime-disable")
			return api.DeviceWatchControlResult{}, err
		}
		c.recordOutcome(capability.DesiredDisabled, configuredScopeIDBestEffort(current), "applied", "")
		return c.controlResult(configuredScopeIDBestEffort(current), result.Changed), nil
	}

	scopeID, err := configuredScopeID(current)
	if err != nil {
		return api.DeviceWatchControlResult{}, err
	}
	proposed := current
	proposed.Desired = capability.DesiredDisabled
	if _, err := c.lifecycle.PreflightConfiguration(ctx, proposed, capability.ActionDisable); err != nil {
		return api.DeviceWatchControlResult{}, mapDeviceWatchLifecycleError(err)
	}
	if err := c.store.InsertCapabilityIntentAudit(ctx, devicewatch.CapabilityID, capability.DesiredDisabled, "requested", scopeID, ""); err != nil {
		return api.DeviceWatchControlResult{}, err
	}
	_, configChanged, err := c.configs.ReplaceCapability(devicewatch.CapabilityID, &proposed)
	if err != nil {
		c.recordOutcome(capability.DesiredDisabled, scopeID, "failed", "config-write")
		return api.DeviceWatchControlResult{}, err
	}
	if err := c.lifecycle.ApplyConfiguration(proposed); err != nil {
		c.emergencyStop()
		c.recordOutcome(capability.DesiredDisabled, scopeID, "failed", "config-apply")
		return api.DeviceWatchControlResult{}, err
	}
	lifecycleResult, err := c.lifecycle.Run(ctx, devicewatch.CapabilityID, capability.ActionDisable)
	if err != nil {
		// Durable desired state intentionally remains disabled. A restart must not
		// resurrect a runtime that the user asked to stop.
		c.emergencyStop()
		c.recordOutcome(capability.DesiredDisabled, scopeID, "failed", "runtime-disable")
		return api.DeviceWatchControlResult{}, err
	}
	c.recordOutcome(capability.DesiredDisabled, scopeID, "applied", "")
	return c.controlResult(scopeID, configChanged || lifecycleResult.Changed), nil
}

func (c *deviceWatchControl) restorePreviousConfiguration(previous *capability.Configuration) error {
	if previous == nil {
		if _, _, err := c.configs.ReplaceCapability(devicewatch.CapabilityID, nil); err != nil {
			return err
		}
		return c.lifecycle.RemoveConfiguration(devicewatch.CapabilityID)
	}
	if _, _, err := c.configs.ReplaceCapability(devicewatch.CapabilityID, previous); err != nil {
		return err
	}
	return c.lifecycle.ApplyConfiguration(*previous)
}

func (c *deviceWatchControl) controlResult(scopeID string, changed bool) api.DeviceWatchControlResult {
	result := api.DeviceWatchControlResult{ScopeID: scopeID, Changed: changed, Active: c.activity.Active()}
	for _, instance := range c.lifecycle.List() {
		if instance.Manifest.ID == devicewatch.CapabilityID {
			result.State = instance.State
			break
		}
	}
	return result
}

func (c *deviceWatchControl) emergencyStop() {
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.activity.Close(stopCtx); err != nil {
		c.logger.Warn("device_watch_emergency_stop_failed")
	}
}

func (c *deviceWatchControl) recordOutcome(desired capability.DesiredState, scopeID, state, reason string) {
	auditCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.store.InsertCapabilityIntentAudit(auditCtx, devicewatch.CapabilityID, desired, state, scopeID, reason); err != nil {
		c.logger.Warn("capability_intent_audit_outcome_failed", "capability", devicewatch.CapabilityID, "state", state)
	}
}

func configuredScopeID(configuration capability.Configuration) (string, error) {
	raw, ok := configuration.Values["network_scope_id"]
	if !ok {
		return "", fmt.Errorf("Device Watch configuration has no network_scope_id")
	}
	var scopeID string
	if err := json.Unmarshal(raw, &scopeID); err != nil || scopeID == "" {
		return "", fmt.Errorf("Device Watch configuration has invalid network_scope_id")
	}
	return scopeID, nil
}

func configuredScopeIDBestEffort(configuration capability.Configuration) string {
	scopeID, _ := configuredScopeID(configuration)
	return scopeID
}

func mapDeviceWatchLifecycleError(err error) error {
	switch {
	case errors.Is(err, capability.ErrPreflightBlocked), errors.Is(err, devicewatch.ErrScopeMismatch), errors.Is(err, devicewatch.ErrPlatformUnsupported):
		return localapi.ErrMutationPrecondition
	case errors.Is(err, storage.ErrNetworkScopeNotFound):
		return localapi.ErrMutationPrecondition
	default:
		return err
	}
}
