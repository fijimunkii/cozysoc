package httpsrun

import (
	"bytes"
	"encoding/json"
	"errors"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"io"
	"time"
	"unicode/utf8"
)

const MaxRetainedEventBytes = 4096

var ErrHistory = errors.New("retained https evidence is invalid or unavailable")

// RetainedRun is historical evidence, never current connectivity or approval.
// Missing phases do not establish whether a request was or was not sent.
type RetainedRun struct {
	RunID, Profile                                             string
	SchemaVersion                                              int
	Selection                                                  nq.HTTPSSelection
	Observer                                                   nq.Observer
	LastAuditAt                                                time.Time
	AuthorizationRetained, AdmissionRetained, TerminalRetained bool
	Outcome, Reason                                            string
	Measurement                                                *Measurement
	Assessment                                                 HistoricalAssessment
}
type HistoricalAssessment struct {
	State, Confidence, Summary, NextStep string
	ExpectationMatched                   *bool
}

func ValidRunID(id string) bool { return runPattern.MatchString(id) }

// DecodeRetainedEvent requires exact unique fields, including the schema-1
// normalized Selection/Observer/Measurement field names. Unknown versions fail closed.
func DecodeRetainedEvent(raw []byte) (Event, error) {
	fields, err := historyObject(raw, []string{"schema_version", "run_id", "state", "at", "profile", "selection", "observer"}, []string{"outcome", "reason", "measurement"})
	if err != nil {
		return Event{}, ErrHistory
	}
	if _, err = historyObject(fields["selection"], []string{"ID", "EndpointID", "RequestID", "Family", "Method", "ExpectedStatus"}, nil); err != nil {
		return Event{}, ErrHistory
	}
	if _, err = historyObject(fields["observer"], []string{"ScopeID", "SensorID", "InterfaceName", "InterfaceIndex"}, nil); err != nil {
		return Event{}, ErrHistory
	}
	if raw, ok := fields["measurement"]; ok {
		if _, err := historyObject(raw, []string{"started_at", "completed_at", "exchange", "request", "stage"}, []string{"gap", "status_code", "response_time_ns"}); err != nil {
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

// DescribeRetainedRun validates at most three phases without inventing siblings.
func DescribeRetainedRun(events []Event, asOf time.Time) (RetainedRun, error) {
	if len(events) < 1 || len(events) > 3 || !validTime(asOf) {
		return RetainedRun{}, ErrHistory
	}
	first := events[0]
	phases := make(map[string]Event, 3)
	latest := first.At
	for _, e := range events {
		if ValidateEvent(e) != nil || e.At.After(asOf) || e.RunID != first.RunID || e.SchemaVersion != first.SchemaVersion || e.Profile != first.Profile || e.Selection != first.Selection || e.Observer != first.Observer {
			return RetainedRun{}, ErrHistory
		}
		if _, ok := phases[e.State]; ok {
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
	auth, authorized := phases["authorized"]
	admit, admitted := phases["admitted"]
	terminal, finished := phases["finished"]
	out := RetainedRun{RunID: first.RunID, Profile: first.Profile, SchemaVersion: first.SchemaVersion, Selection: first.Selection, Observer: first.Observer, LastAuditAt: latest.Round(0).UTC(), AuthorizationRetained: authorized, AdmissionRetained: admitted, TerminalRetained: finished, Outcome: "unknown"}
	if finished {
		out.Outcome, out.Reason = terminal.Outcome, terminal.Reason
		if terminal.Measurement != nil {
			m := terminal.Measurement.sample(first.RunID, first.Selection, first.Observer)
			if (authorized && m.StartedAt.Before(auth.At)) || (admitted && m.StartedAt.Before(admit.At)) {
				return RetainedRun{}, ErrHistory
			}
			out.Measurement = auditMeasurement(&m)
		}
	}
	out.Assessment = HistoricalAssessment{State: "unknown", Confidence: "unknown", Summary: "No terminal audit is retained for this run; its outcome is unknown.", NextStep: "Inspect retained evidence without repeating traffic. Missing records do not prove that nothing was sent."}
	if !finished {
		return out, nil
	}
	if out.Measurement == nil {
		out.Assessment.Summary = "The retained terminal audit contains no usable HTTPS measurement."
		return out, nil
	}
	m := out.Measurement.sample(first.RunID, first.Selection, first.Observer)
	// Assess at the ORIGINAL sample end, never at read time. Do not publish the
	// dynamic State/FreshUntil fields, which would misrepresent history as current.
	report, err := nq.AssessHTTPS(nq.HTTPSSnapshot{Observer: m.Observer, Selections: []nq.HTTPSSelection{m.Selection}, AsOf: m.CompletedAt, WindowStart: m.StartedAt, Freshness: time.Second, Measurements: []nq.HTTPSMeasurement{m}})
	if err != nil || len(report.Checks) != 1 {
		return RetainedRun{}, ErrHistory
	}
	c := report.Checks[0]
	out.Assessment = HistoricalAssessment{State: string(c.Result), Confidence: string(c.Confidence), Summary: "Recorded sample: " + c.Summary, NextStep: "Compare evidence from the same time; this does not establish current connectivity or security. " + c.NextStep}
	if c.ExpectationMatched != nil {
		matched := *c.ExpectationMatched
		out.Assessment.ExpectationMatched = &matched
	}
	return out, nil
}
