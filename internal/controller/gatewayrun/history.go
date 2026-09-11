package gatewayrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"
)

const MaxRetainedEventBytes = 4096

var ErrHistory = errors.New("retained gateway evidence is invalid or unavailable")

// RetainedRun describes recorded history, never current connectivity or approval.
// Missing phases may reflect in-flight work, interruption, expiry or deletion;
// their absence is not evidence that execution did or did not happen.
type RetainedRun struct {
	RunID, ScopeID, Profile, InterfaceName, Target, Source     string
	InterfaceIndex, SchemaVersion                              int
	LastAuditAt                                                time.Time
	AuthorizationRetained, AdmissionRetained, TerminalRetained bool
	Outcome, Reason                                            string
	Measurement                                                *Measurement
	Assessment                                                 HistoricalAssessment
}

// HistoricalAssessment describes only the retained sample at its original time.
// Even recent evidence is historical here; no current/fresh network state exists.
type HistoricalAssessment struct {
	State, Confidence, Summary, NextStep string
	ReplyLossPercent                     *float64
}

func ValidRunID(id string) bool { return hexID.MatchString(id) }

// DecodeRetainedEvent bounds bytes and accepts only exact, unique schema fields.
// It is separate from general JSON decoding and never echoes persisted input.
func DecodeRetainedEvent(raw []byte) (Event, error) {
	fields, err := historyObject(raw,
		[]string{"schema_version", "run_id", "state", "at", "profile", "selection_digest", "scope_id", "interface_name", "interface_index", "target", "source"},
		[]string{"outcome", "reason", "measurement"})
	if err != nil {
		return Event{}, ErrHistory
	}
	if m, ok := fields["measurement"]; ok {
		if _, err := historyObject(m, []string{"started_at", "send_calls", "accepted_requests", "replies", "timeouts", "complete"}, []string{"completed_at", "mean_rtt_ns"}); err != nil {
			return Event{}, ErrHistory
		}
	}
	var e Event
	if json.Unmarshal(raw, &e) != nil || ValidateEvent(e) != nil {
		return Event{}, ErrHistory
	}
	return e, nil
}

func historyObject(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxRetainedEventBytes || !utf8.Valid(raw) {
		return nil, ErrHistory
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, name := range append(append([]string(nil), required...), optional...) {
		allowed[name] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, ErrHistory
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || !allowed[name] || fields[name] != nil {
			return nil, ErrHistory
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrHistory
		}
		fields[name] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return nil, ErrHistory
	}
	for _, name := range required {
		if fields[name] == nil {
			return nil, ErrHistory
		}
	}
	return fields, nil
}

// DescribeRetainedRun folds at most the three durable phases of exactly one run.
// Callers enforce storage retention/scope. Missing siblings are not reconstructed.
func DescribeRetainedRun(events []Event, asOf time.Time) (RetainedRun, error) {
	if len(events) < 1 || len(events) > 3 || !validTime(asOf) {
		return RetainedRun{}, ErrHistory
	}
	first := events[0]
	phases := make(map[string]Event, 3)
	latest := first.At
	for _, e := range events {
		if ValidateEvent(e) != nil || e.At.After(asOf) || e.RunID != first.RunID || e.ScopeID != first.ScopeID ||
			e.SchemaVersion != first.SchemaVersion || e.Profile != first.Profile || e.SelectionDigest != first.SelectionDigest ||
			e.InterfaceName != first.InterfaceName || e.InterfaceIndex != first.InterfaceIndex || e.Source != first.Source || e.Target != first.Target {
			return RetainedRun{}, ErrHistory
		}
		if _, exists := phases[e.State]; exists {
			return RetainedRun{}, ErrHistory
		}
		phases[e.State] = e
		if e.At.After(latest) {
			latest = e.At
		}
	}
	var previous time.Time
	for _, name := range []string{"authorized", "admitted", "finished"} {
		if e, ok := phases[name]; ok {
			if !previous.IsZero() && e.At.Before(previous) {
				return RetainedRun{}, ErrHistory
			}
			previous = e.At
		}
	}
	_, authorized := phases["authorized"]
	admission, admitted := phases["admitted"]
	terminal, finished := phases["finished"]
	out := RetainedRun{RunID: first.RunID, ScopeID: first.ScopeID, Profile: first.Profile,
		SchemaVersion: first.SchemaVersion, InterfaceName: first.InterfaceName, InterfaceIndex: first.InterfaceIndex,
		Target: first.Target, Source: first.Source, LastAuditAt: latest.Round(0).UTC(),
		AuthorizationRetained: authorized, AdmissionRetained: admitted, TerminalRetained: finished, Outcome: "unknown"}
	if finished {
		out.Outcome, out.Reason = terminal.Outcome, terminal.Reason
		if terminal.Measurement != nil {
			m := *terminal.Measurement
			if (admitted && m.StartedAt.Before(admission.At)) || (authorized && m.StartedAt.Before(phases["authorized"].At)) {
				return RetainedRun{}, ErrHistory
			}
			if m.CompletedAt != nil {
				at := *m.CompletedAt
				m.CompletedAt = &at
			}
			if m.MeanRTTNanoseconds != nil {
				ns := *m.MeanRTTNanoseconds
				m.MeanRTTNanoseconds = &ns
			}
			out.Measurement = &m
		}
	}
	out.Assessment = describeHistoricalSample(out)
	return out, nil
}

func describeHistoricalSample(r RetainedRun) HistoricalAssessment {
	a := HistoricalAssessment{State: "unknown", Confidence: "unknown",
		Summary:  "No terminal audit is retained for this run; its outcome is unknown.",
		NextStep: "Inspect retained evidence later without repeating traffic. A missing record does not prove that nothing was sent."}
	if !r.TerminalRetained {
		return a
	}
	if r.SchemaVersion == 1 {
		a.Summary = "This legacy audit records execution only, not a connectivity measurement."
		a.NextStep = "Do not interpret an execution-only completion as a reply or a successful network check."
		return a
	}
	m := r.Measurement
	if m == nil {
		a.Summary = "This terminal audit contains no usable connectivity measurement."
		a.NextStep = "Review the recorded execution outcome; missing evidence cannot establish connectivity or an outage."
		return a
	}
	if !m.Complete {
		a.State = "incomplete"
		a.Summary = "The retained sample is incomplete; partial counts do not establish reply loss or latency."
		a.NextStep = "Unsent requests are not timeouts. Review the execution outcome before considering a separately authorized check."
		return a
	}
	a.Confidence = "limited"
	loss := 100 * float64(m.Timeouts) / float64(m.AcceptedRequests)
	a.ReplyLossPercent = &loss
	a.NextStep = "Compare other evidence from the same time. This historical ICMP sample proves neither current connectivity, gateway identity, an internet outage, nor security posture."
	switch m.Replies {
	case 3:
		a.State, a.Summary = "all-replied", "The selected target answered all three requests during this recorded sample."
	case 0:
		a.State, a.Summary = "no-replies", "The selected target did not answer this recorded sample; ICMP filtering or target behavior may explain it."
	default:
		a.State, a.Summary = "some-replies", "The selected target answered some requests during this recorded sample."
	}
	return a
}
