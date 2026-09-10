package networkquality

import "time"

type State string

const (
	StateUnknown       State = "unknown"
	StateNotMeasured   State = "not-measured"
	StateStale         State = "stale"
	StateSucceeded     State = "check-succeeded"
	StateIssueObserved State = "issue-observed"
)

type Confidence string

const (
	ConfidenceUnknown Confidence = "unknown"
	ConfidenceLimited Confidence = "limited"
)

// CheckAssessment describes one target/method/family at one observation point.
// Confidence is intentionally limited even for a successful check: this first
// contract neither corroborates root causes nor makes an internet-wide verdict.
type CheckAssessment struct {
	Target           Target
	State            State
	Confidence       Confidence
	EvidenceID       string
	StartedAt        time.Time
	CompletedAt      time.Time
	FreshUntil       time.Time
	Gap              GapReason
	Attempts         int
	Successes        int
	MeanLatency      *time.Duration
	ReplyLossPercent *float64
	Summary          string
	NextStep         string
}

// Report has no global quality score, security verdict, or coverage state.
// An empty check list means no checks selected, never a healthy network.
type Report struct {
	Observer    Observer
	AsOf        time.Time
	WindowStart time.Time
	Checks      []CheckAssessment
	Limitations []string
}

// Assess is deterministic, side-effect-free, and independent of measurement order.
// It selects by evidence completion time, not insertion order or replay time.
// More recent explicit gaps supersede prior successes; missing data never
// creates a failed check or an inferred sleep/offline event.
func Assess(snapshot Snapshot) (Report, error) {
	if err := ValidateSnapshot(snapshot); err != nil {
		return Report{}, err
	}
	latest := make(map[string]Measurement, len(snapshot.Targets))
	for _, measurement := range snapshot.Measurements {
		previous, exists := latest[measurement.TargetID]
		if !exists || measurement.CompletedAt.After(previous.CompletedAt) {
			latest[measurement.TargetID] = measurement
		}
	}
	report := Report{
		Observer: snapshot.Observer, AsOf: snapshot.AsOf, WindowStart: snapshot.WindowStart,
		Checks: make([]CheckAssessment, 0, len(snapshot.Targets)),
		Limitations: []string{
			"These measurements describe one observing device and interface, not the whole home.",
			"Network quality is separate from security findings and monitoring coverage.",
			"One failed check does not establish an internet outage; targets or protocols may be unavailable or filtered.",
		},
	}
	for _, target := range snapshot.Targets {
		measurement, exists := latest[target.ID]
		check := CheckAssessment{
			Target: target, State: StateUnknown, Confidence: ConfidenceUnknown,
			Summary:  "No measurement is available for this check.",
			NextStep: "Review setup before collecting a fresh, explicitly authorized measurement.",
		}
		if exists {
			check = assessMeasurement(target, measurement, snapshot.AsOf, snapshot.Freshness)
		}
		report.Checks = append(report.Checks, check)
	}
	return report, nil
}

func assessMeasurement(target Target, measurement Measurement, asOf time.Time, freshness time.Duration) CheckAssessment {
	check := CheckAssessment{
		Target: target, State: StateStale, Confidence: ConfidenceUnknown,
		EvidenceID: measurement.ID, StartedAt: measurement.StartedAt, CompletedAt: measurement.CompletedAt,
		FreshUntil: measurement.CompletedAt.Add(freshness),
		Gap:        measurement.Gap, Attempts: measurement.Attempts, Successes: measurement.Successes,
		Summary:  "This check has only historical evidence.",
		NextStep: "Collect a fresh authorized measurement; a gap does not establish a network outage.",
	}
	// Validity is half-open. At exactly fresh-until, evidence is historical.
	if !check.FreshUntil.After(asOf) {
		return check
	}
	if measurement.Outcome == OutcomeUnavailable || measurement.Outcome == OutcomeNotRun {
		check.State = StateNotMeasured
		check.Summary, check.NextStep = gapCopy(measurement.Gap)
		return check
	}
	check.Confidence = ConfidenceLimited
	check.State = StateSucceeded
	if measurement.Outcome != OutcomeSucceeded {
		check.State = StateIssueObserved
	}
	if measurement.MeanLatency != nil {
		latency := *measurement.MeanLatency
		check.MeanLatency = &latency
	}
	if target.Method == MethodICMP {
		loss := 100 * float64(measurement.Attempts-measurement.Successes) / float64(measurement.Attempts)
		check.ReplyLossPercent = &loss
	}
	check.Summary, check.NextStep = measurementCopy(target, measurement.Outcome)
	return check
}

func gapCopy(reason GapReason) (string, string) {
	switch reason {
	case GapPermission:
		return "This check could not run with the available permissions.", "Review the permission requirement; no network failure was measured."
	case GapUnsupported:
		return "This measurement is unavailable on the current platform.", "Use a supported measurement source; do not interpret missing support as poor connectivity."
	case GapSourceUnavailable:
		return "The measurement source was unavailable.", "Check the measurement source and retry without changing network configuration."
	case GapDisabled:
		return "This check is disabled.", "Leave it disabled, or review its scope and privacy impact before enabling it."
	case GapNotConfigured:
		return "This check has no configured destination.", "Select an authorized destination only when this measurement is needed."
	case GapSleep:
		return "No measurement was taken during the recorded controller sleep gap.", "Collect fresh evidence after resume; the gap is not a measured outage."
	case GapOffline:
		return "No measurement was taken during the recorded controller offline gap.", "Restore the controller and collect fresh evidence; network connectivity during the gap is unknown."
	case GapNetworkChanged:
		return "The network binding changed, so this check did not run.", "Review network enrollment before collecting more evidence."
	default:
		// ValidateSnapshot rejects unknown reasons before this function is used.
		return "This check was not measured.", "Review the measurement source before retrying."
	}
}

func measurementCopy(target Target, outcome Outcome) (string, string) {
	if target.Layer == LayerLink {
		if outcome == OutcomeSucceeded {
			return "The local interface reported up.", "Interface state alone does not establish gateway, DNS, or internet reachability."
		}
		return "The local interface reported down.", "Review this device's local connection; other devices and paths were not assessed."
	}
	if outcome == OutcomePartial {
		if target.Method == MethodICMP {
			return "Some ICMP probes were unanswered.", "Compare a later bounded sample; filtering or target load can affect probe replies."
		}
		if target.Method == MethodDNS {
			return "Some DNS checks to the selected resolver failed.", "Review resolver evidence; failed DNS checks are not a packet-loss measurement."
		}
		return "Some HTTPS checks to the selected target failed.", "Compare other authorized evidence before inferring a wider connectivity problem."
	}
	switch target.Layer {
	case LayerGateway:
		if outcome == OutcomeSucceeded {
			return "The selected gateway answered the ICMP check.", "This result does not establish DNS or external connectivity."
		}
		return "The selected gateway did not answer this ICMP check.", "Review the gateway and local connection; ICMP may be filtered."
	case LayerDNS:
		if outcome == OutcomeSucceeded {
			return "The selected resolver completed the DNS check.", "This result is limited to the selected resolver and query, not all DNS or internet traffic."
		}
		return "The DNS check to the selected resolver failed.", "Review resolver settings and corroborating evidence before changing DNS configuration."
	default:
		if outcome == OutcomeSucceeded {
			return "The selected external target passed this check.", "This result is limited to this target, protocol, address family, and observation point."
		}
		return "The selected external target did not pass this check.", "Compare other authorized evidence before concluding there is a wider internet problem."
	}
}
