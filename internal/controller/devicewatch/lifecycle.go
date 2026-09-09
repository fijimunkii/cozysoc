package devicewatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type RuntimeControl interface {
	Start(context.Context, string) error
	Stop(context.Context) (bool, error)
	Running() bool
	State() RuntimeState
	IngestionHealth() storage.IngestionHealth
}

type LifecycleDriver struct {
	store     *storage.Store
	runtime   RuntimeControl
	inspector InterfaceInspector
	platform  string
	now       func() time.Time
}

func NewLifecycleDriver(store *storage.Store, ingestor *storage.Ingestor, logger *slog.Logger) (*LifecycleDriver, error) {
	if store == nil || ingestor == nil {
		return nil, fmt.Errorf("device watch lifecycle requires storage and ingestion")
	}
	driver := &LifecycleDriver{
		store:     store,
		inspector: NewSystemInterfaceInspector(),
		platform:  goruntime.GOOS,
		now:       time.Now,
	}
	if goruntime.GOOS == "darwin" {
		runtime, err := NewRuntime(store, ingestor, logger)
		if err != nil {
			return nil, err
		}
		driver.runtime = runtime
	}
	return driver, nil
}

func newLifecycleDriver(store *storage.Store, runtime RuntimeControl, inspector InterfaceInspector, platform string) (*LifecycleDriver, error) {
	if store == nil || inspector == nil || platform == "" {
		return nil, fmt.Errorf("device watch lifecycle test dependencies are required")
	}
	return &LifecycleDriver{store: store, runtime: runtime, inspector: inspector, platform: platform, now: time.Now}, nil
}

func (d *LifecycleDriver) Preflight(ctx context.Context, request capability.DriverRequest) (capability.PreflightReport, error) {
	if request.Action == capability.ActionDisable {
		return capability.PreflightReport{Checks: []capability.PreflightCheck{{
			ID: "runtime-disable", Status: capability.CheckPass, Blocking: true,
		}}}, nil
	}

	checks := []capability.PreflightCheck{{
		ID:       "platform-supported",
		Status:   capability.CheckPass,
		Blocking: true,
	}}
	if d.platform != "darwin" {
		checks[0].Status = capability.CheckFail
		checks[0].Message = "passive Device Watch runtime is not supported on this platform"
		return capability.PreflightReport{Checks: checks}, nil
	}

	scopeID, err := scopeIDFromConfiguration(request.Configuration)
	if err != nil {
		checks = append(checks, capability.PreflightCheck{
			ID: "scope-configured", Status: capability.CheckFail, Blocking: true,
			Message: "Device Watch requires an enrolled network scope",
		})
		return capability.PreflightReport{Checks: checks}, nil
	}
	checks = append(checks, capability.PreflightCheck{ID: "scope-configured", Status: capability.CheckPass, Blocking: true})

	scope, err := d.store.GetNetworkScope(ctx, scopeID)
	if errors.Is(err, storage.ErrNetworkScopeNotFound) {
		checks = append(checks, capability.PreflightCheck{
			ID: "scope-enrolled", Status: capability.CheckFail, Blocking: true,
			Message: "enrolled Device Watch network scope is unavailable",
		})
		return capability.PreflightReport{Checks: checks}, nil
	}
	if err != nil {
		return capability.PreflightReport{}, err
	}
	binding, err := ParseScopeBinding(scope)
	if err != nil {
		checks = append(checks, capability.PreflightCheck{
			ID: "scope-enrolled", Status: capability.CheckFail, Blocking: true,
			Message: "enrolled Device Watch scope is invalid",
		})
		return capability.PreflightReport{Checks: checks}, nil
	}
	checks = append(checks, capability.PreflightCheck{ID: "scope-enrolled", Status: capability.CheckPass, Blocking: true})

	if _, err := ValidateCurrentScope(ctx, d.inspector, binding); err != nil {
		checks = append(checks, capability.PreflightCheck{
			ID: "scope-current", Status: capability.CheckFail, Blocking: true,
			Message: "enrolled network no longer matches the current interface",
		})
		return capability.PreflightReport{Checks: checks}, nil
	}
	checks = append(checks, capability.PreflightCheck{ID: "scope-current", Status: capability.CheckPass, Blocking: true})
	return capability.PreflightReport{Checks: checks}, nil
}

func (d *LifecycleDriver) Execute(ctx context.Context, action capability.LifecycleAction, request capability.DriverRequest) (capability.StepResult, error) {
	switch action {
	case capability.ActionEnable:
		if d.runtime == nil {
			return capability.StepResult{}, ErrPlatformUnsupported
		}
		scopeID, err := scopeIDFromConfiguration(request.Configuration)
		if err != nil {
			return capability.StepResult{}, err
		}
		if d.runtime.Running() {
			if d.runtime.State().ScopeID == scopeID {
				return capability.StepResult{Changed: false}, nil
			}
			return capability.StepResult{}, fmt.Errorf("device watch runtime is active for a different scope")
		}
		if err := d.runtime.Start(ctx, scopeID); err != nil {
			return capability.StepResult{}, err
		}
		return capability.StepResult{Changed: true}, nil
	case capability.ActionDisable:
		if d.runtime == nil {
			return capability.StepResult{Changed: false}, nil
		}
		changed, err := d.runtime.Stop(ctx)
		return capability.StepResult{Changed: changed}, err
	default:
		return capability.StepResult{}, fmt.Errorf("device watch lifecycle does not execute %s", action)
	}
}

func (d *LifecycleDriver) Verify(ctx context.Context, request capability.DriverRequest) (capability.VerificationReport, error) {
	scopeStatus := capability.SignalMissing
	observationSignal := capability.VerificationSignal{
		ID:      "observation-freshness",
		Status:  capability.SignalMissing,
		Message: "no current Device Watch coverage evidence is available",
	}
	now := time.Now().UTC()
	if d.now != nil {
		now = d.now().UTC()
	}
	operational, operationalErr := d.OperationalHealth(ctx, now)
	if operationalErr != nil {
		return capability.VerificationReport{}, operationalErr
	}

	if scopeID, err := scopeIDFromConfiguration(request.Configuration); err == nil {
		if scope, getErr := d.store.GetNetworkScope(ctx, scopeID); getErr == nil {
			if binding, parseErr := ParseScopeBinding(scope); parseErr == nil {
				if _, currentErr := ValidateCurrentScope(ctx, d.inspector, binding); currentErr == nil {
					scopeStatus = capability.SignalFresh
				}
			}
			coverageSignal, coverageErr := coverageVerificationSignal(ctx, d.store, scopeID, now)
			if coverageErr != nil {
				return capability.VerificationReport{}, coverageErr
			}
			observationSignal = coverageSignal
		} else if !errors.Is(getErr, storage.ErrNetworkScopeNotFound) {
			return capability.VerificationReport{}, getErr
		}
	}
	return capability.VerificationReport{Signals: []capability.VerificationSignal{
		{ID: "network-scope-enrolled", Status: scopeStatus},
		observationSignal,
		sensorVerificationSignal(operational.Sensor),
		pipelineVerificationSignal(operational.Pipeline),
		databaseVerificationSignal(operational.Database),
	}}, nil
}

func (d *LifecycleDriver) Close(ctx context.Context) error {
	if d == nil || d.runtime == nil {
		return nil
	}
	_, err := d.runtime.Stop(ctx)
	return err
}

func scopeIDFromConfiguration(configuration capability.Configuration) (string, error) {
	raw, ok := configuration.Values["network_scope_id"]
	if !ok {
		return "", fmt.Errorf("device watch configuration has no network_scope_id")
	}
	var scopeID string
	if err := json.Unmarshal(raw, &scopeID); err != nil || scopeID == "" {
		return "", fmt.Errorf("device watch configuration has invalid network_scope_id")
	}
	return scopeID, nil
}
