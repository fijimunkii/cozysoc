// Package resolverrun admits one explicitly reviewed DNS exchange. Its compiled
// collaborators are trusted controller code, never client-supplied transports.
// The controller owns one inert coordinator; no product execution endpoint
// exposes it yet. The isolated lab supplies a native sender for actual runs.
package resolverrun

import (
	"context"
	"errors"
	"regexp"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
)

var (
	ErrUnavailable = errors.New("resolver run control is unavailable")
	ErrBusy        = errors.New("resolver run control is busy")
	ErrCooldown    = errors.New("resolver run cooldown is active")
	ErrConsent     = errors.New("explicit one-shot resolver consent is required")
	ErrReview      = errors.New("resolver review is unknown, consumed, or expired")
	ErrPreflight   = errors.New("resolver run preflight is unavailable or changed")
	ErrAudit       = errors.New("resolver run audit could not be confirmed; run control is locked")
	ErrClock       = errors.New("resolver run clock is invalid; run control is locked")
	ErrExecution   = errors.New("resolver execution did not complete; approval is consumed")
)

const (
	Profile            = resolverplan.Profile
	EventSchemaVersion = 1
	RunInterval        = time.Minute
	OperationTimeout   = 5 * time.Second
	AuditTimeout       = time.Second
)

var idPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
var runPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func validID(s string) bool      { return len(s) > 0 && len(s) <= 128 && idPattern.MatchString(s) }
func validTime(t time.Time) bool { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }

// Route evidence must precede construction of the source-bound plan. It is
// metadata only; the executor must independently check its actual socket binding.
type Selection struct {
	Plan            resolverplan.Plan
	RouteObservedAt time.Time
	RouteFreshUntil time.Time
}

// Preflight resolves an opaque configured selection ID locally, never via DNS.
// Revalidation must re-read current configuration/enrollment and inspect routing.
type Preflight func(context.Context, string) (Selection, error)
type Request struct {
	Selection     Selection
	MeasurementID string
}
type Executor interface {
	ExecuteResolver(context.Context, Request) (nq.ResolverMeasurement, error)
}
type Auditor interface {
	InsertResolverRunAudit(context.Context, Event) error
}
type Ticket struct{ key [32]byte }

func (Ticket) String() string               { return "[resolver review ticket]" }
func (Ticket) GoString() string             { return "[resolver review ticket]" }
func (Ticket) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

type Review struct {
	Ticket    Ticket
	Selection Selection
	ExpiresAt time.Time
}

// Completed means a validated response or completed timeout and committed audit,
// not successful resolution, internet reachability, coverage or security.
type Result struct {
	RunID   string
	Outcome string
	Sample  *nq.ResolverMeasurement
}

// Event retains opaque immutable configuration references and normalized evidence.
// No endpoint, question, raw answer, private ticket or exception text is persisted.
// Configuration owners must preserve immutable reference attribution separately;
// this audit stream cannot reconstruct a deleted configuration or grant authority.
type Event struct {
	SchemaVersion int                  `json:"schema_version"`
	RunID         string               `json:"run_id"`
	State         string               `json:"state"`
	Outcome       string               `json:"outcome,omitempty"`
	Reason        string               `json:"reason,omitempty"`
	At            time.Time            `json:"at"`
	Profile       string               `json:"profile"`
	Selection     nq.ResolverSelection `json:"selection"`
	Observer      nq.Observer          `json:"observer"`
	Measurement   *Measurement         `json:"measurement,omitempty"`
}

func ValidateEvent(e Event) error {
	if e.SchemaVersion != EventSchemaVersion || !runPattern.MatchString(e.RunID) || e.Profile != Profile || !validTime(e.At) || e.Selection.Transport != nq.DNSUDP {
		return ErrAudit
	}
	snapshot := nq.ResolverSnapshot{Observer: e.Observer, Selections: []nq.ResolverSelection{e.Selection}, AsOf: e.At, WindowStart: e.At, Freshness: time.Second}
	if e.Measurement != nil {
		m := e.Measurement.sample(e.RunID, e.Selection, e.Observer)
		if e.State != "finished" || (e.Outcome != "completed" && e.Outcome != "failed" && e.Outcome != "canceled") || validateMeasurement(m, e.At) != nil {
			return ErrAudit
		}
		if e.Outcome == "completed" && !complete(m) {
			return ErrAudit
		}
		snapshot.WindowStart = m.StartedAt
		snapshot.Measurements = []nq.ResolverMeasurement{m}
	}
	if nq.ValidateResolverSnapshot(snapshot) != nil {
		return ErrAudit
	}
	switch e.State {
	case "authorized", "admitted":
		if e.Outcome == "" && e.Reason == "" && e.Measurement == nil {
			return nil
		}
	case "finished":
		switch e.Outcome {
		case "completed":
			if e.Reason == "" && e.Measurement != nil {
				return nil
			}
		case "blocked":
			if e.Measurement == nil && (e.Reason == "preflight-unavailable" || e.Reason == "selection-changed" || e.Reason == "review-expired") {
				return nil
			}
		case "canceled":
			if e.Reason == "canceled" {
				return nil
			}
		case "failed":
			if e.Reason == "execution-error" || (e.Reason == "measurement-invalid" && e.Measurement == nil) {
				return nil
			}
		case "indeterminate":
			if e.Reason == "execution-panic" && e.Measurement == nil {
				return nil
			}
		}
	}
	return ErrAudit
}

func normalize(s Selection, id string, now time.Time) (Selection, error) {
	d := s.Plan.Disclosure()
	if !s.Plan.Current(now) || d.Configuration.Selection.ID != id || !validTime(s.RouteObservedAt) ||
		s.RouteObservedAt.After(d.CreatedAt) || !s.RouteFreshUntil.Equal(s.RouteObservedAt.Add(resolverplan.ReviewLifetime)) || !s.RouteFreshUntil.After(now) {
		return Selection{}, ErrPreflight
	}
	return s, nil
}
func copySelection(s Selection) Selection { return s } // Plan owns immutable private data.
