package coverage

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	maxObservationPoints = 32
	maxDimensions        = 128
	maxSources           = 32
	maxDirections        = 8
	maxGaps              = 32
	maxValueLength       = 256
	maxSummaryLength     = 256
	maxDetailLength      = 1024
	maxNextStepLength    = 512
)

var tokenPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// State is the user-facing state of a capability or one observation point.
// It deliberately has no percentage or implicit protection meaning.
type State string

const (
	StateUnconfigured       State = "unconfigured"
	StateUnavailable        State = "unavailable"
	StatePermissionRequired State = "permission-required"
	StateUnverified         State = "unverified"
	StateActiveLimited      State = "active-limited"
	StateDegraded           State = "degraded"
	StateStale              State = "stale"
	StateDisconnected       State = "disconnected"
)

type SourceState string

const (
	SourceExpectedUnverified SourceState = "expected-unverified"
	SourceCurrent            SourceState = "current"
	SourcePermissionRequired SourceState = "permission-required"
	SourceUnavailable        SourceState = "unavailable"
	SourceDegraded           SourceState = "degraded"
	SourceStale              SourceState = "stale"
	SourceDisconnected       SourceState = "disconnected"
	SourceUnknown            SourceState = "unknown"
)

type DimensionKind string

const (
	DimensionNetwork         DimensionKind = "network"
	DimensionInterface       DimensionKind = "interface"
	DimensionVLAN            DimensionKind = "vlan"
	DimensionDevice          DimensionKind = "device"
	DimensionAddressFamily   DimensionKind = "address-family"
	DimensionWirelessBand    DimensionKind = "wireless-band"
	DimensionWirelessChannel DimensionKind = "wireless-channel"
)

type Direction string

const (
	DirectionIngress         Direction = "ingress"
	DirectionEgress          Direction = "egress"
	DirectionEastWest        Direction = "east-west"
	DirectionClientToService Direction = "client-to-service"
	DirectionServiceToClient Direction = "service-to-client"
)

type CadenceMode string

const (
	CadenceUnknown     CadenceMode = "unknown"
	CadenceContinuous  CadenceMode = "continuous"
	CadencePeriodic    CadenceMode = "periodic"
	CadenceEventDriven CadenceMode = "event-driven"
	CadenceHopping     CadenceMode = "hopping"
)

type Dimension struct {
	Kind  DimensionKind
	Value string
}

// Scope keeps user/configuration expectation distinct from evidence. Verified
// dimensions may be discovered dynamically (for example observed DNS clients),
// while ExpectedUnverified dimensions must also be part of Configured.
type Scope struct {
	Configured         []Dimension
	Verified           []Dimension
	ExpectedUnverified []Dimension
}

type Source struct {
	ID       string
	Kind     string
	State    SourceState
	Expected bool
	Observed bool
	NextStep string
}

type EvidenceWindow struct {
	HasEvidence bool
	StartedAt   time.Time
	EndedAt     time.Time
	FreshUntil  time.Time
}

// Cadence describes how an observation point samples evidence. Dwell is used
// only by hopping observation points; it prevents channel hopping from being
// represented as continuous all-channel coverage.
type Cadence struct {
	Mode     CadenceMode
	Interval time.Duration
	Dwell    time.Duration
}

type Gap struct {
	ID         string
	Kind       string
	Summary    string
	Detail     string
	NextStep   string
	Dimensions []Dimension
	Directions []Direction
}

type ObservationPoint struct {
	ID         string
	Kind       string
	SensorID   string
	State      State
	Reason     string
	Scope      Scope
	Sources    []Source
	Directions []Direction
	Window     EvidenceWindow
	Cadence    Cadence
	Gaps       []Gap
	NextStep   string
}

type Report struct {
	CapabilityID      string
	Configured        bool
	State             State
	Reason            string
	ObservationPoints []ObservationPoint
	NextStep          string
}

func ValidateReport(report Report) error {
	if !validToken(report.CapabilityID) {
		return fmt.Errorf("coverage capability_id is invalid")
	}
	if !validState(report.State) {
		return fmt.Errorf("coverage state %q is invalid", report.State)
	}
	if !validToken(report.Reason) {
		return fmt.Errorf("coverage reason is invalid")
	}
	if err := validateText("coverage next step", report.NextStep, maxNextStepLength); err != nil {
		return err
	}
	if !report.Configured {
		if report.State != StateUnconfigured {
			return fmt.Errorf("unconfigured coverage must use state %q", StateUnconfigured)
		}
		if len(report.ObservationPoints) != 0 {
			return fmt.Errorf("unconfigured coverage cannot contain observation points")
		}
		return nil
	}
	if report.State == StateUnconfigured {
		return fmt.Errorf("configured coverage cannot use state %q", StateUnconfigured)
	}
	if len(report.ObservationPoints) == 0 || len(report.ObservationPoints) > maxObservationPoints {
		return fmt.Errorf("configured coverage must contain 1..%d observation points", maxObservationPoints)
	}

	seenPoints := make(map[string]struct{}, len(report.ObservationPoints))
	for index, point := range report.ObservationPoints {
		if err := validateObservationPoint(point); err != nil {
			return fmt.Errorf("observation point %d: %w", index, err)
		}
		if _, exists := seenPoints[point.ID]; exists {
			return fmt.Errorf("duplicate observation point %q", point.ID)
		}
		seenPoints[point.ID] = struct{}{}
	}
	return nil
}

func validateObservationPoint(point ObservationPoint) error {
	if !validToken(point.ID) || !validToken(point.Kind) {
		return fmt.Errorf("id or kind is invalid")
	}
	if point.SensorID != "" && !validToken(point.SensorID) {
		return fmt.Errorf("sensor_id is invalid")
	}
	if !validState(point.State) || point.State == StateUnconfigured {
		return fmt.Errorf("state %q is invalid for a configured observation point", point.State)
	}
	if !validToken(point.Reason) {
		return fmt.Errorf("reason is invalid")
	}
	if err := validateText("next step", point.NextStep, maxNextStepLength); err != nil {
		return err
	}
	if err := validateScope(point.Scope); err != nil {
		return err
	}
	if err := validateSources(point.Sources); err != nil {
		return err
	}
	if err := validateDirections(point.Directions); err != nil {
		return err
	}
	if err := validateWindow(point.Window); err != nil {
		return err
	}
	if err := validateCadence(point.Cadence); err != nil {
		return err
	}
	if len(point.Gaps) > maxGaps {
		return fmt.Errorf("too many gaps")
	}
	seenGaps := make(map[string]struct{}, len(point.Gaps))
	for _, gap := range point.Gaps {
		if err := validateGap(gap); err != nil {
			return err
		}
		if _, exists := seenGaps[gap.ID]; exists {
			return fmt.Errorf("duplicate gap %q", gap.ID)
		}
		seenGaps[gap.ID] = struct{}{}
	}
	return nil
}

func validateScope(scope Scope) error {
	configured, err := validateDimensionList("configured scope", scope.Configured)
	if err != nil {
		return err
	}
	verified, err := validateDimensionList("verified scope", scope.Verified)
	if err != nil {
		return err
	}
	expected, err := validateDimensionList("expected-unverified scope", scope.ExpectedUnverified)
	if err != nil {
		return err
	}
	for key := range expected {
		if _, ok := configured[key]; !ok {
			return fmt.Errorf("expected-unverified dimension %q is not configured", key)
		}
		if _, ok := verified[key]; ok {
			return fmt.Errorf("dimension %q cannot be both verified and expected-unverified", key)
		}
	}
	return nil
}

func validateDimensionList(name string, dimensions []Dimension) (map[string]struct{}, error) {
	if len(dimensions) > maxDimensions {
		return nil, fmt.Errorf("%s has too many dimensions", name)
	}
	seen := make(map[string]struct{}, len(dimensions))
	for _, dimension := range dimensions {
		if !validDimensionKind(dimension.Kind) {
			return nil, fmt.Errorf("%s contains invalid kind %q", name, dimension.Kind)
		}
		if err := validateValue("coverage dimension", dimension.Value); err != nil {
			return nil, err
		}
		key := string(dimension.Kind) + "\x00" + dimension.Value
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s contains duplicate dimension %q", name, dimension.Value)
		}
		seen[key] = struct{}{}
	}
	return seen, nil
}

func validateSources(sources []Source) error {
	if len(sources) > maxSources {
		return fmt.Errorf("too many coverage sources")
	}
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if !validToken(source.ID) || !validToken(source.Kind) || !validSourceState(source.State) {
			return fmt.Errorf("coverage source is invalid")
		}
		if _, exists := seen[source.ID]; exists {
			return fmt.Errorf("duplicate coverage source %q", source.ID)
		}
		seen[source.ID] = struct{}{}
		if source.State != SourceCurrent {
			if err := validateText("coverage source next step", source.NextStep, maxNextStepLength); err != nil {
				return err
			}
		} else if source.NextStep != "" {
			if err := validateOptionalText("coverage source next step", source.NextStep, maxNextStepLength); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDirections(directions []Direction) error {
	if len(directions) > maxDirections {
		return fmt.Errorf("too many coverage directions")
	}
	seen := make(map[Direction]struct{}, len(directions))
	for _, direction := range directions {
		if !validDirection(direction) {
			return fmt.Errorf("coverage direction %q is invalid", direction)
		}
		if _, exists := seen[direction]; exists {
			return fmt.Errorf("duplicate coverage direction %q", direction)
		}
		seen[direction] = struct{}{}
	}
	return nil
}

func validateWindow(window EvidenceWindow) error {
	if !window.HasEvidence {
		if !window.StartedAt.IsZero() || !window.EndedAt.IsZero() || !window.FreshUntil.IsZero() {
			return fmt.Errorf("coverage window without evidence must not contain timestamps")
		}
		return nil
	}
	if window.StartedAt.IsZero() || window.EndedAt.IsZero() || window.FreshUntil.IsZero() {
		return fmt.Errorf("coverage evidence window requires started, ended, and fresh-until timestamps")
	}
	if window.EndedAt.Before(window.StartedAt) || window.FreshUntil.Before(window.EndedAt) {
		return fmt.Errorf("coverage evidence window timestamps are inconsistent")
	}
	return nil
}

func validateCadence(cadence Cadence) error {
	if cadence.Interval < 0 || cadence.Dwell < 0 {
		return fmt.Errorf("coverage cadence durations cannot be negative")
	}
	switch cadence.Mode {
	case CadenceUnknown, CadenceContinuous, CadenceEventDriven:
		if cadence.Interval != 0 || cadence.Dwell != 0 {
			return fmt.Errorf("coverage cadence %q cannot carry interval or dwell", cadence.Mode)
		}
	case CadencePeriodic:
		if cadence.Interval <= 0 || cadence.Dwell != 0 {
			return fmt.Errorf("periodic coverage cadence requires a positive interval and no dwell")
		}
	case CadenceHopping:
		if cadence.Dwell <= 0 {
			return fmt.Errorf("hopping coverage cadence requires a positive dwell")
		}
		if cadence.Interval > 0 && cadence.Interval < cadence.Dwell {
			return fmt.Errorf("hopping coverage interval cannot be shorter than dwell")
		}
	default:
		return fmt.Errorf("coverage cadence mode %q is invalid", cadence.Mode)
	}
	return nil
}

func validateGap(gap Gap) error {
	if !validToken(gap.ID) || !validToken(gap.Kind) {
		return fmt.Errorf("coverage gap id or kind is invalid")
	}
	if err := validateText("coverage gap summary", gap.Summary, maxSummaryLength); err != nil {
		return err
	}
	if err := validateText("coverage gap detail", gap.Detail, maxDetailLength); err != nil {
		return err
	}
	if err := validateText("coverage gap next step", gap.NextStep, maxNextStepLength); err != nil {
		return err
	}
	if _, err := validateDimensionList("coverage gap dimensions", gap.Dimensions); err != nil {
		return err
	}
	return validateDirections(gap.Directions)
}

func validState(state State) bool {
	switch state {
	case StateUnconfigured, StateUnavailable, StatePermissionRequired, StateUnverified, StateActiveLimited, StateDegraded, StateStale, StateDisconnected:
		return true
	default:
		return false
	}
}

func validSourceState(state SourceState) bool {
	switch state {
	case SourceExpectedUnverified, SourceCurrent, SourcePermissionRequired, SourceUnavailable, SourceDegraded, SourceStale, SourceDisconnected, SourceUnknown:
		return true
	default:
		return false
	}
}

func validDimensionKind(kind DimensionKind) bool {
	switch kind {
	case DimensionNetwork, DimensionInterface, DimensionVLAN, DimensionDevice, DimensionAddressFamily, DimensionWirelessBand, DimensionWirelessChannel:
		return true
	default:
		return false
	}
}

func validDirection(direction Direction) bool {
	switch direction {
	case DirectionIngress, DirectionEgress, DirectionEastWest, DirectionClientToService, DirectionServiceToClient:
		return true
	default:
		return false
	}
}

func validToken(value string) bool {
	return len(value) > 0 && len(value) <= maxValueLength && tokenPattern.MatchString(value)
}

func validateValue(name, value string) error {
	if value == "" || len(value) > maxValueLength || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func validateText(name, value string, maxLength int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return validateOptionalText(name, value, maxLength)
}

func validateOptionalText(name, value string, maxLength int) error {
	if len(value) > maxLength || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00") {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}
