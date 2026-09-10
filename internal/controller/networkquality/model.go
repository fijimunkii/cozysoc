// Package networkquality assesses bounded, already-collected quality evidence.
// It performs no I/O, schedules no probes, and grants no network authority.
// Its results are neither security findings nor monitoring-coverage states.
package networkquality

import (
	"fmt"
	"regexp"
	"time"
)

const (
	MaxTargets      = 16
	MaxMeasurements = 128
	MaxWindow       = 24 * time.Hour
	MaxFreshness    = 15 * time.Minute
	MaxCheckTime    = 30 * time.Second
	MaxAttempts     = 10
)

var tokenPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
var interfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

type Layer string

const (
	LayerLink     Layer = "local-link"
	LayerGateway  Layer = "gateway"
	LayerDNS      Layer = "dns"
	LayerExternal Layer = "external-target"
)

type Method string

const (
	MethodInterface Method = "interface-state"
	MethodICMP      Method = "icmp-echo"
	MethodDNS       Method = "dns-query"
	MethodHTTPS     Method = "https-request"
)

type AddressFamily string

const (
	FamilyNone AddressFamily = "not-applicable"
	FamilyIPv4 AddressFamily = "ipv4"
	FamilyIPv6 AddressFamily = "ipv6"
)

// Observer identifies the measurement location, not proof of authorization.
// A future collector must independently revalidate the enrolled binding before
// every operation; matching these fields alone does not grant permission.
type Observer struct {
	ScopeID        string
	SensorID       string
	InterfaceName  string
	InterfaceIndex int
}

// Target is a reference to controller-selected configuration, never an address,
// URL, command, or instruction to execute. IPv4 and IPv6 checks stay separate.
type Target struct {
	ID     string
	Layer  Layer
	Method Method
	Family AddressFamily
}

type Outcome string

const (
	OutcomeSucceeded   Outcome = "succeeded"
	OutcomePartial     Outcome = "partial"
	OutcomeFailed      Outcome = "failed"
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeNotRun      Outcome = "not-run"
)

type GapReason string

const (
	GapNone              GapReason = ""
	GapPermission        GapReason = "permission-required"
	GapUnsupported       GapReason = "unsupported"
	GapSourceUnavailable GapReason = "source-unavailable"
	GapDisabled          GapReason = "disabled"
	GapNotConfigured     GapReason = "not-configured"
	GapSleep             GapReason = "controller-sleep"
	GapOffline           GapReason = "controller-offline"
	GapNetworkChanged    GapReason = "network-changed"
)

// Measurement is normalized evidence, not a raw protocol response. Successes
// counts successful checks, not received packets: a DNS error response is not a
// successful DNS check. Only ICMP counts can produce a probe-reply-loss metric.
// MeanLatency is optional and describes successful checks only.
// Gap reasons require positive runtime evidence; absence alone cannot prove sleep.
type Measurement struct {
	ID          string
	TargetID    string
	Observer    Observer
	StartedAt   time.Time
	CompletedAt time.Time
	Outcome     Outcome
	Gap         GapReason
	Attempts    int
	Successes   int
	MeanLatency *time.Duration
}

// Snapshot is one bounded assessment input, not a history store or scheduling
// policy. The caller supplies the assessment time and evidence-time freshness.
// Ingestion/replay time is deliberately absent from this contract.
type Snapshot struct {
	Observer     Observer
	AsOf         time.Time
	WindowStart  time.Time
	Freshness    time.Duration
	Targets      []Target
	Measurements []Measurement
}

func ValidateSnapshot(snapshot Snapshot) error {
	if !validObserver(snapshot.Observer) {
		return fmt.Errorf("quality observer is invalid")
	}
	if snapshot.AsOf.IsZero() || snapshot.WindowStart.IsZero() ||
		snapshot.WindowStart.After(snapshot.AsOf) || snapshot.AsOf.Sub(snapshot.WindowStart) > MaxWindow {
		return fmt.Errorf("quality assessment window is invalid")
	}
	if snapshot.Freshness < time.Second || snapshot.Freshness > MaxFreshness {
		return fmt.Errorf("quality freshness must be between one second and fifteen minutes")
	}
	if len(snapshot.Targets) > MaxTargets || len(snapshot.Measurements) > MaxMeasurements {
		return fmt.Errorf("quality input exceeds collection bounds")
	}
	targets := make(map[string]Target, len(snapshot.Targets))
	for _, target := range snapshot.Targets {
		if err := validateTarget(target); err != nil {
			return err
		}
		if _, exists := targets[target.ID]; exists {
			return fmt.Errorf("duplicate quality target")
		}
		targets[target.ID] = target
	}
	seen := make(map[string]struct{}, len(snapshot.Measurements))
	// Reject tied measurements rather than choose a contradictory result by
	// input order. UTC canonicalization also catches equivalent time offsets.
	ends := make(map[string]map[time.Time]struct{}, len(targets))
	for _, measurement := range snapshot.Measurements {
		if !validToken(measurement.ID) {
			return fmt.Errorf("quality measurement id is invalid")
		}
		if _, exists := seen[measurement.ID]; exists {
			return fmt.Errorf("duplicate quality measurement")
		}
		seen[measurement.ID] = struct{}{}
		target, exists := targets[measurement.TargetID]
		if !exists || measurement.Observer != snapshot.Observer {
			return fmt.Errorf("quality measurement is outside the selected context")
		}
		if measurement.StartedAt.IsZero() || measurement.CompletedAt.IsZero() ||
			measurement.StartedAt.Before(snapshot.WindowStart) || measurement.CompletedAt.After(snapshot.AsOf) ||
			measurement.CompletedAt.Before(measurement.StartedAt) {
			return fmt.Errorf("quality measurement timestamps are invalid")
		}
		end := measurement.CompletedAt.Round(0).UTC()
		if ends[target.ID] == nil {
			ends[target.ID] = make(map[time.Time]struct{})
		}
		if _, exists := ends[target.ID][end]; exists {
			return fmt.Errorf("quality target has ambiguous simultaneous measurements")
		}
		ends[target.ID][end] = struct{}{}
		if err := validateMeasurement(target, measurement); err != nil {
			return err
		}
	}
	return nil
}

func validateTarget(target Target) error {
	if !validToken(target.ID) {
		return fmt.Errorf("quality target id is invalid")
	}
	if target.Layer == LayerLink {
		if target.Method == MethodInterface && target.Family == FamilyNone {
			return nil
		}
		return fmt.Errorf("local link requires interface-state with no address family")
	}
	if target.Family != FamilyIPv4 && target.Family != FamilyIPv6 {
		return fmt.Errorf("quality target address family is invalid")
	}
	switch target.Layer {
	case LayerGateway:
		if target.Method == MethodICMP {
			return nil
		}
	case LayerDNS:
		if target.Method == MethodDNS {
			return nil
		}
	case LayerExternal:
		if target.Method == MethodICMP || target.Method == MethodHTTPS {
			return nil
		}
	}
	return fmt.Errorf("quality layer and method combination is invalid")
}

func validateMeasurement(target Target, measurement Measurement) error {
	switch measurement.Outcome {
	case OutcomeUnavailable, OutcomeNotRun:
		if measurement.Attempts != 0 || measurement.Successes != 0 || measurement.MeanLatency != nil {
			return fmt.Errorf("unmeasured quality check cannot carry metrics")
		}
		if measurement.Outcome == OutcomeUnavailable {
			switch measurement.Gap {
			case GapPermission, GapUnsupported, GapSourceUnavailable:
				return nil
			}
		} else {
			switch measurement.Gap {
			case GapDisabled, GapNotConfigured, GapSleep, GapOffline, GapNetworkChanged:
				return nil
			}
		}
		return fmt.Errorf("unmeasured quality check requires a matching gap reason")
	case OutcomeSucceeded, OutcomePartial, OutcomeFailed:
		if measurement.CompletedAt.Sub(measurement.StartedAt) > MaxCheckTime {
			return fmt.Errorf("quality check exceeds duration bound")
		}
		if measurement.Gap != GapNone {
			return fmt.Errorf("measured quality check cannot carry a gap reason")
		}
	default:
		return fmt.Errorf("quality measurement outcome is invalid")
	}
	if target.Method == MethodInterface {
		if measurement.Outcome == OutcomePartial || measurement.Attempts != 0 || measurement.Successes != 0 || measurement.MeanLatency != nil {
			return fmt.Errorf("interface-state cannot carry probe metrics or a partial outcome")
		}
		return nil
	}
	if measurement.Attempts < 1 || measurement.Attempts > MaxAttempts ||
		measurement.Successes < 0 || measurement.Successes > measurement.Attempts {
		return fmt.Errorf("quality check counts are invalid")
	}
	if (measurement.Outcome == OutcomeSucceeded && measurement.Successes != measurement.Attempts) ||
		(measurement.Outcome == OutcomePartial && (measurement.Successes == 0 || measurement.Successes == measurement.Attempts)) ||
		(measurement.Outcome == OutcomeFailed && measurement.Successes != 0) {
		return fmt.Errorf("quality outcome contradicts check counts")
	}
	if measurement.MeanLatency != nil {
		if measurement.Successes == 0 || *measurement.MeanLatency < 0 ||
			*measurement.MeanLatency > measurement.CompletedAt.Sub(measurement.StartedAt) {
			return fmt.Errorf("quality latency is inconsistent with successful checks")
		}
	}
	return nil
}

func validToken(value string) bool {
	return len(value) > 0 && len(value) <= 128 && tokenPattern.MatchString(value)
}

func validObserver(observer Observer) bool {
	return validToken(observer.ScopeID) && validToken(observer.SensorID) &&
		len(observer.InterfaceName) > 0 && len(observer.InterfaceName) <= 64 &&
		interfacePattern.MatchString(observer.InterfaceName) && observer.InterfaceIndex > 0
}
