package capability

import (
	"context"
	"fmt"
)

// PreflightConfiguration evaluates a proposed durable configuration without
// mutating instance or runtime state. Callers can therefore reject unsupported
// enablement before persisting user intent.
func (e *LifecycleEngine) PreflightConfiguration(ctx context.Context, configuration Configuration, action LifecycleAction) (PreflightReport, error) {
	if e == nil || e.instances == nil || e.instances.registry == nil {
		return PreflightReport{}, fmt.Errorf("capability lifecycle engine is unavailable")
	}
	if err := ValidateConfiguration(e.instances.registry, configuration); err != nil {
		return PreflightReport{}, err
	}
	instance, ok := e.instances.Get(configuration.ID)
	if !ok {
		return PreflightReport{}, fmt.Errorf("unknown capability %q", configuration.ID)
	}
	if !hasLifecycleAction(instance.Manifest, action) {
		return PreflightReport{}, fmt.Errorf("%w: %s does not declare %s", ErrActionNotDeclared, configuration.ID, action)
	}
	if !ownershipAllowsAction(configuration.Ownership, action) {
		return PreflightReport{}, fmt.Errorf("%w: %s ownership %s cannot perform %s", ErrOwnershipAction, configuration.ID, configuration.Ownership, action)
	}
	if err := desiredAllowsAction(configuration.Desired, action); err != nil {
		return PreflightReport{}, fmt.Errorf("%w: %s", ErrDesiredStateMismatch, err)
	}
	driver, ok := e.drivers[configuration.ID]
	if !ok {
		return PreflightReport{}, fmt.Errorf("%w: %s", ErrDriverUnavailable, configuration.ID)
	}

	operationLock := e.operationLock(configuration.ID)
	operationLock.Lock()
	defer operationLock.Unlock()

	request := DriverRequest{
		Manifest:      instance.Manifest,
		Configuration: cloneConfiguration(configuration),
		Ownership:     configuration.Ownership,
		Action:        action,
	}
	report, _, err := e.callPreflight(ctx, driver, request)
	if err != nil {
		return PreflightReport{}, err
	}
	if !report.Ready() {
		return report, fmt.Errorf("%w: %s %s", ErrPreflightBlocked, configuration.ID, action)
	}
	return report, nil
}

// ApplyConfiguration updates the in-memory view only after its durable form has
// been committed by the controller configuration store. It never performs the
// lifecycle action itself.
func (e *LifecycleEngine) ApplyConfiguration(configuration Configuration) error {
	if e == nil || e.instances == nil || e.instances.registry == nil {
		return fmt.Errorf("capability lifecycle engine is unavailable")
	}
	if err := ValidateConfiguration(e.instances.registry, configuration); err != nil {
		return err
	}
	instance, ok := e.instances.Get(configuration.ID)
	if !ok {
		return fmt.Errorf("unknown capability %q", configuration.ID)
	}

	operationLock := e.operationLock(configuration.ID)
	operationLock.Lock()
	defer operationLock.Unlock()

	previous := e.currentState(configuration.ID, instance.State)
	if err := e.instances.SetConfiguration(configuration); err != nil {
		return err
	}
	next := previous
	next.Desired = configuration.Desired
	if previous.Desired != configuration.Desired {
		next.Verification = VerificationUnverified
	}
	if err := next.Validate(instance.Manifest); err != nil {
		return fmt.Errorf("invalid configured state for %s: %w", configuration.ID, err)
	}
	e.stateMu.Lock()
	e.states[configuration.ID] = next
	e.stateMu.Unlock()
	return nil
}

// RemoveConfiguration restores the manifest default durable intent in memory.
// This is used only to compensate a failed first-time configuration change.
func (e *LifecycleEngine) RemoveConfiguration(id string) error {
	if e == nil || e.instances == nil || e.instances.registry == nil {
		return fmt.Errorf("capability lifecycle engine is unavailable")
	}
	instance, ok := e.instances.Get(id)
	if !ok {
		return fmt.Errorf("unknown capability %q", id)
	}

	operationLock := e.operationLock(id)
	operationLock.Lock()
	defer operationLock.Unlock()

	if err := e.instances.RemoveConfiguration(id); err != nil {
		return err
	}
	previous := e.currentState(id, instance.State)
	next := previous
	next.Desired = DesiredDisabled
	next.Verification = VerificationUnverified
	if err := next.Validate(instance.Manifest); err != nil {
		return fmt.Errorf("invalid default state for %s: %w", id, err)
	}
	e.stateMu.Lock()
	e.states[id] = next
	e.stateMu.Unlock()
	return nil
}
