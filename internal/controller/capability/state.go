package capability

import "fmt"

type DesiredState string

const (
	DesiredDisabled DesiredState = "disabled"
	DesiredEnabled  DesiredState = "enabled"
)

type ProcessState string

const (
	ProcessNotApplicable ProcessState = "not-applicable"
	ProcessStopped       ProcessState = "stopped"
	ProcessStarting      ProcessState = "starting"
	ProcessRunning       ProcessState = "running"
	ProcessFailed        ProcessState = "failed"
)

type VerificationState string

const (
	VerificationUnverified VerificationState = "unverified"
	VerificationVerifying  VerificationState = "verifying"
	VerificationVerified   VerificationState = "verified"
	VerificationDegraded   VerificationState = "degraded"
	VerificationStale      VerificationState = "stale"
)

type InstanceState struct {
	Desired      DesiredState      `json:"desired"`
	Process      ProcessState      `json:"process"`
	Verification VerificationState `json:"verification"`
}

func (s InstanceState) Validate(manifest Manifest) error {
	if s.Desired != DesiredDisabled && s.Desired != DesiredEnabled {
		return fmt.Errorf("unknown desired state %q", s.Desired)
	}
	switch s.Process {
	case ProcessNotApplicable, ProcessStopped, ProcessStarting, ProcessRunning, ProcessFailed:
	default:
		return fmt.Errorf("unknown process state %q", s.Process)
	}
	switch s.Verification {
	case VerificationUnverified, VerificationVerifying, VerificationVerified, VerificationDegraded, VerificationStale:
	default:
		return fmt.Errorf("unknown verification state %q", s.Verification)
	}
	if containsOwnership(manifest.Ownership, OwnershipBuiltin) && s.Process != ProcessNotApplicable {
		return fmt.Errorf("builtin capability %q has no independent process state", manifest.ID)
	}
	return nil
}
