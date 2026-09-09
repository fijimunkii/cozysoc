package capability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultLifecycleMaxAttempts = 3
	defaultLifecycleRetryDelay  = 100 * time.Millisecond
)

var (
	ErrPreflightBlocked     = errors.New("capability preflight blocked the action")
	ErrActionNotDeclared    = errors.New("capability lifecycle action is not declared")
	ErrOwnershipAction      = errors.New("capability ownership does not permit the action")
	ErrDesiredStateMismatch = errors.New("capability desired state does not permit the action")
	ErrDriverUnavailable    = errors.New("capability lifecycle driver is unavailable")
)

type CheckStatus string

const (
	CheckPass    CheckStatus = "pass"
	CheckFail    CheckStatus = "fail"
	CheckUnknown CheckStatus = "unknown"
)

type PreflightCheck struct {
	ID       string      `json:"id"`
	Status   CheckStatus `json:"status"`
	Blocking bool        `json:"blocking"`
	Message  string      `json:"message,omitempty"`
}

type PreflightReport struct {
	Checks []PreflightCheck `json:"checks"`
}

func (r PreflightReport) Ready() bool {
	for _, check := range r.Checks {
		if check.Blocking && check.Status != CheckPass {
			return false
		}
	}
	return true
}

type VerificationSignalStatus string

const (
	SignalFresh   VerificationSignalStatus = "fresh"
	SignalMissing VerificationSignalStatus = "missing"
	SignalStale   VerificationSignalStatus = "stale"
	SignalFailed  VerificationSignalStatus = "failed"
)

type VerificationSignal struct {
	ID      string                   `json:"id"`
	Status  VerificationSignalStatus `json:"status"`
	Message string                   `json:"message,omitempty"`
}

type VerificationReport struct {
	Signals []VerificationSignal `json:"signals"`
}

type DriverRequest struct {
	Manifest      Manifest
	Configuration Configuration
	Ownership     OwnershipMode
	Action        LifecycleAction
}

type StepResult struct {
	Changed bool
	Process *ProcessState
}

type LifecycleDriver interface {
	Preflight(context.Context, DriverRequest) (PreflightReport, error)
	Execute(context.Context, LifecycleAction, DriverRequest) (StepResult, error)
	Verify(context.Context, DriverRequest) (VerificationReport, error)
}

type LifecycleResult struct {
	Action            LifecycleAction     `json:"action"`
	Attempts          int                 `json:"attempts"`
	PreflightAttempts int                 `json:"preflight_attempts,omitempty"`
	Changed           bool                `json:"changed"`
	State             InstanceState       `json:"state"`
	Preflight         *PreflightReport    `json:"preflight,omitempty"`
	Verification      *VerificationReport `json:"verification,omitempty"`
}

type LifecycleEngine struct {
	instances *Instances
	drivers   map[string]LifecycleDriver
	logger    *slog.Logger

	stateMu sync.RWMutex
	states  map[string]InstanceState

	lockMu sync.Mutex
	locks  map[string]*sync.Mutex

	maxAttempts int
	retryDelay  time.Duration
}

func NewLifecycleEngine(instances *Instances, drivers map[string]LifecycleDriver, logger *slog.Logger) (*LifecycleEngine, error) {
	if instances == nil || instances.registry == nil {
		return nil, fmt.Errorf("capability instances are required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	engine := &LifecycleEngine{
		instances:   instances,
		drivers:     make(map[string]LifecycleDriver, len(drivers)),
		logger:      logger,
		states:      make(map[string]InstanceState),
		locks:       make(map[string]*sync.Mutex),
		maxAttempts: defaultLifecycleMaxAttempts,
		retryDelay:  defaultLifecycleRetryDelay,
	}
	for _, instance := range instances.List() {
		engine.states[instance.Manifest.ID] = instance.State
	}
	for id, driver := range drivers {
		if driver == nil {
			return nil, fmt.Errorf("capability %q has a nil lifecycle driver", id)
		}
		if _, ok := instances.Get(id); !ok {
			return nil, fmt.Errorf("lifecycle driver registered for unknown capability %q", id)
		}
		engine.drivers[id] = driver
	}
	return engine, nil
}

func (e *LifecycleEngine) List() []Instance {
	if e == nil || e.instances == nil {
		return nil
	}
	instances := e.instances.List()
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	for index := range instances {
		if state, ok := e.states[instances[index].Manifest.ID]; ok {
			instances[index].State = state
		}
	}
	return instances
}

func (e *LifecycleEngine) Run(ctx context.Context, id string, action LifecycleAction) (LifecycleResult, error) {
	var result LifecycleResult
	result.Action = action
	if e == nil || e.instances == nil {
		return result, fmt.Errorf("capability lifecycle engine is unavailable")
	}

	operationLock := e.operationLock(id)
	operationLock.Lock()
	defer operationLock.Unlock()

	instance, configuration, err := e.operationSnapshot(id)
	if err != nil {
		return result, err
	}

	result.State = e.currentState(id, instance.State)
	if !hasLifecycleAction(instance.Manifest, action) {
		return result, fmt.Errorf("%w: %s does not declare %s", ErrActionNotDeclared, id, action)
	}
	if !ownershipAllowsAction(instance.Ownership, action) {
		return result, fmt.Errorf("%w: %s ownership %s cannot perform %s", ErrOwnershipAction, id, instance.Ownership, action)
	}
	if err := desiredAllowsAction(configuration.Desired, action); err != nil {
		return result, fmt.Errorf("%w: %s", ErrDesiredStateMismatch, err)
	}

	driver, ok := e.drivers[id]
	if !ok {
		return result, fmt.Errorf("%w: %s", ErrDriverUnavailable, id)
	}
	request := DriverRequest{
		Manifest:      instance.Manifest,
		Configuration: cloneConfiguration(configuration),
		Ownership:     instance.Ownership,
		Action:        action,
	}

	e.logger.Debug("capability_lifecycle_started", "capability", id, "action", action)
	switch action {
	case ActionPreflight:
		report, attempts, callErr := e.callPreflight(ctx, driver, request)
		result.Attempts = attempts
		if callErr != nil {
			e.logOutcome(id, action, "error")
			return result, callErr
		}
		result.Preflight = &report
		e.logOutcome(id, action, "complete")
		return result, nil
	case ActionVerify:
		report, attempts, callErr := e.callVerify(ctx, driver, request)
		result.Attempts = attempts
		if callErr != nil {
			e.logOutcome(id, action, "error")
			return result, callErr
		}
		verificationState, evalErr := evaluateVerification(instance.Manifest, report)
		if evalErr != nil {
			e.logOutcome(id, action, "error")
			return result, evalErr
		}
		next := result.State
		next.Verification = verificationState
		if err := e.setState(id, instance.Manifest, next); err != nil {
			return result, err
		}
		result.State = next
		result.Verification = &report
		e.logOutcome(id, action, "complete")
		return result, nil
	default:
		preflightRequest := request
		preflightRequest.Action = action
		report, attempts, callErr := e.callPreflight(ctx, driver, preflightRequest)
		result.PreflightAttempts = attempts
		if callErr != nil {
			e.logOutcome(id, action, "error")
			return result, callErr
		}
		result.Preflight = &report
		if !report.Ready() {
			e.logOutcome(id, action, "blocked")
			return result, fmt.Errorf("%w: %s %s", ErrPreflightBlocked, id, action)
		}

		step, executeAttempts, executeErr := e.callExecute(ctx, driver, request)
		result.Attempts = executeAttempts
		if executeErr != nil {
			e.logOutcome(id, action, "error")
			return result, executeErr
		}
		next := result.State
		if step.Process != nil {
			next.Process = *step.Process
		}
		next.Verification = VerificationUnverified
		if err := e.setState(id, instance.Manifest, next); err != nil {
			return result, err
		}
		result.State = next
		result.Changed = step.Changed
		e.logOutcome(id, action, "complete")
		return result, nil
	}
}

func (e *LifecycleEngine) callPreflight(ctx context.Context, driver LifecycleDriver, request DriverRequest) (PreflightReport, int, error) {
	report, attempts, err := retryCall(ctx, e.maxAttempts, e.retryDelay, func() (PreflightReport, error) {
		return driver.Preflight(ctx, request)
	}, func(attempt int) {
		e.logger.Debug("capability_lifecycle_retry", "capability", request.Manifest.ID, "action", request.Action, "attempt", attempt)
	})
	if err != nil {
		return report, attempts, err
	}
	if err := validatePreflightReport(report); err != nil {
		return PreflightReport{}, attempts, fmt.Errorf("invalid preflight report for %s: %w", request.Manifest.ID, err)
	}
	return report, attempts, nil
}

func (e *LifecycleEngine) callExecute(ctx context.Context, driver LifecycleDriver, request DriverRequest) (StepResult, int, error) {
	return retryCall(ctx, e.maxAttempts, e.retryDelay, func() (StepResult, error) {
		return driver.Execute(ctx, request.Action, request)
	}, func(attempt int) {
		e.logger.Debug("capability_lifecycle_retry", "capability", request.Manifest.ID, "action", request.Action, "attempt", attempt)
	})
}

func (e *LifecycleEngine) callVerify(ctx context.Context, driver LifecycleDriver, request DriverRequest) (VerificationReport, int, error) {
	report, attempts, err := retryCall(ctx, e.maxAttempts, e.retryDelay, func() (VerificationReport, error) {
		return driver.Verify(ctx, request)
	}, func(attempt int) {
		e.logger.Debug("capability_lifecycle_retry", "capability", request.Manifest.ID, "action", request.Action, "attempt", attempt)
	})
	if err != nil {
		return report, attempts, err
	}
	if err := validateVerificationReport(report); err != nil {
		return VerificationReport{}, attempts, fmt.Errorf("invalid verification report for %s: %w", request.Manifest.ID, err)
	}
	return report, attempts, nil
}

func (e *LifecycleEngine) operationSnapshot(id string) (Instance, Configuration, error) {
	instance, ok := e.instances.Get(id)
	if !ok {
		return Instance{}, Configuration{}, fmt.Errorf("unknown capability %q", id)
	}
	configuration, ok := e.instances.Configuration(id)
	if !ok {
		configuration = defaultConfiguration(instance.Manifest)
	}
	return instance, configuration, nil
}

func (e *LifecycleEngine) currentState(id string, fallback InstanceState) InstanceState {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	if state, ok := e.states[id]; ok {
		return state
	}
	return fallback
}

func (e *LifecycleEngine) setState(id string, manifest Manifest, state InstanceState) error {
	if err := state.Validate(manifest); err != nil {
		return fmt.Errorf("invalid runtime state for %s: %w", id, err)
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	previous, ok := e.states[id]
	if ok && previous.Desired != state.Desired {
		return fmt.Errorf("lifecycle execution cannot change durable desired state for %s", id)
	}
	e.states[id] = state
	return nil
}

func (e *LifecycleEngine) operationLock(id string) *sync.Mutex {
	e.lockMu.Lock()
	defer e.lockMu.Unlock()
	lock, ok := e.locks[id]
	if !ok {
		lock = &sync.Mutex{}
		e.locks[id] = lock
	}
	return lock
}

func (e *LifecycleEngine) logOutcome(id string, action LifecycleAction, outcome string) {
	e.logger.Debug("capability_lifecycle_finished", "capability", id, "action", action, "outcome", outcome)
}

func hasLifecycleAction(manifest Manifest, action LifecycleAction) bool {
	for _, declared := range manifest.Lifecycle {
		if declared == action {
			return true
		}
	}
	return false
}

func ownershipAllowsAction(ownership OwnershipMode, action LifecycleAction) bool {
	switch ownership {
	case OwnershipBuiltin:
		return action == ActionPreflight || action == ActionEnable || action == ActionVerify || action == ActionDisable
	case OwnershipExternal:
		switch action {
		case ActionPreflight, ActionConnect, ActionEnable, ActionVerify, ActionDisable, ActionDisconnect:
			return true
		default:
			return false
		}
	case OwnershipManagedLocal, OwnershipManagedRemote:
		return true
	default:
		return false
	}
}

func desiredAllowsAction(desired DesiredState, action LifecycleAction) error {
	switch action {
	case ActionPreflight:
		return nil
	case ActionInstall, ActionConnect, ActionStart, ActionEnable, ActionVerify, ActionUpgrade:
		if desired != DesiredEnabled {
			return fmt.Errorf("%s requires desired state %s", action, DesiredEnabled)
		}
	case ActionDisable, ActionStop, ActionDisconnect, ActionUninstall:
		if desired != DesiredDisabled {
			return fmt.Errorf("%s requires desired state %s", action, DesiredDisabled)
		}
	default:
		return fmt.Errorf("unknown lifecycle action %q", action)
	}
	return nil
}

func validatePreflightReport(report PreflightReport) error {
	seen := make(map[string]bool, len(report.Checks))
	for _, check := range report.Checks {
		if err := validateID("preflight check id", check.ID); err != nil {
			return err
		}
		if seen[check.ID] {
			return fmt.Errorf("duplicate preflight check %q", check.ID)
		}
		seen[check.ID] = true
		switch check.Status {
		case CheckPass, CheckFail, CheckUnknown:
		default:
			return fmt.Errorf("unknown preflight status %q", check.Status)
		}
		if len(check.Message) > 240 || hasUnsafeText(check.Message) {
			return fmt.Errorf("preflight check %q message is invalid", check.ID)
		}
	}
	return nil
}

func validateVerificationReport(report VerificationReport) error {
	seen := make(map[string]bool, len(report.Signals))
	for _, signal := range report.Signals {
		if err := validateID("verification signal", signal.ID); err != nil {
			return err
		}
		if seen[signal.ID] {
			return fmt.Errorf("duplicate verification signal %q", signal.ID)
		}
		seen[signal.ID] = true
		switch signal.Status {
		case SignalFresh, SignalMissing, SignalStale, SignalFailed:
		default:
			return fmt.Errorf("unknown verification signal status %q", signal.Status)
		}
		if len(signal.Message) > 240 || hasUnsafeText(signal.Message) {
			return fmt.Errorf("verification signal %q message is invalid", signal.ID)
		}
	}
	return nil
}

func evaluateVerification(manifest Manifest, report VerificationReport) (VerificationState, error) {
	expected := make(map[string]bool, len(manifest.Health.VerificationSignals))
	for _, id := range manifest.Health.VerificationSignals {
		expected[id] = true
	}

	observed := make(map[string]VerificationSignalStatus, len(report.Signals))
	for _, signal := range report.Signals {
		if !expected[signal.ID] {
			return VerificationUnverified, fmt.Errorf("verification report contains undeclared signal %q", signal.ID)
		}
		observed[signal.ID] = signal.Status
	}

	state := VerificationVerified
	for _, id := range manifest.Health.VerificationSignals {
		status, ok := observed[id]
		if !ok || status == SignalMissing {
			if state == VerificationVerified {
				state = VerificationUnverified
			}
			continue
		}
		switch status {
		case SignalFailed:
			state = VerificationDegraded
		case SignalStale:
			if state != VerificationDegraded {
				state = VerificationStale
			}
		case SignalFresh:
		}
	}
	return state, nil
}

type retryableError struct {
	err error
}

func (e retryableError) Error() string {
	return e.err.Error()
}

func (e retryableError) Unwrap() error {
	return e.err
}

func (retryableError) Retryable() bool {
	return true
}

func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return retryableError{err: err}
}

func IsRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var marker interface{ Retryable() bool }
	return errors.As(err, &marker) && marker.Retryable()
}

func retryCall[T any](ctx context.Context, maxAttempts int, retryDelay time.Duration, call func() (T, error), onRetry func(int)) (T, int, error) {
	var zero T
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, attempt - 1, err
		}
		value, err := call()
		if err == nil {
			return value, attempt, nil
		}
		if !IsRetryable(err) || attempt == maxAttempts {
			return zero, attempt, err
		}
		if onRetry != nil {
			onRetry(attempt + 1)
		}
		if err := waitForRetry(ctx, retryDelay); err != nil {
			return zero, attempt, err
		}
	}
	return zero, maxAttempts, fmt.Errorf("lifecycle retry loop exhausted")
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
