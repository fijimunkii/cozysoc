package httpstcp

import (
	"context"
	"regexp"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var measurementID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Request is trusted controller-owned input after consumed admission, not a wire
// format or consent ticket. The run coordinator must allocate a fresh identity.
type Request = httpsrun.Request

var _ httpsrun.Executor = (*Candidate)(nil)

// ExecuteHTTPS produces normalized evidence for a connection attempt. Rejected
// preflight/admission returns no measurement; the coordinator must preserve its
// own reason rather than invent a network failure. It neither persists evidence
// nor creates authority. Nonzero measurements may accompany normalized failures.
func (c *Candidate) ExecuteHTTPS(ctx context.Context, request Request) (nq.HTTPSMeasurement, error) {
	if !measurementID.MatchString(request.MeasurementID) {
		return nq.HTTPSMeasurement{}, ErrBinding
	}
	r, err := c.Execute(ctx, request.Selection)
	if r.Stage == "" {
		return nq.HTTPSMeasurement{}, err
	}
	d := request.Selection.Plan.Disclosure()
	m := nq.HTTPSMeasurement{ID: request.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, Stage: r.Stage, Exchange: r.Exchange, Request: r.Request, StatusCode: r.StatusCode}
	if r.Exchange == nq.HTTPSResponseReceived {
		if err != nil || r.ResponseReceivedAt.IsZero() || r.ResponseReceivedAt.Before(r.StartedAt) || r.ResponseReceivedAt.After(r.CompletedAt) {
			return nq.HTTPSMeasurement{}, ErrUnavailable
		}
		elapsed := r.ResponseReceivedAt.Sub(r.StartedAt)
		m.ResponseTime = &elapsed
	}
	// Check signed time bounds, ordering, reference ownership and stage/outcome
	// consistency through the same contract consumed by history and comparison.
	snapshot := nq.HTTPSSnapshot{Observer: m.Observer, AsOf: m.CompletedAt, WindowStart: m.StartedAt, Freshness: time.Second, Selections: []nq.HTTPSSelection{m.Selection}, Measurements: []nq.HTTPSMeasurement{m}}
	if nq.ValidateHTTPSSnapshot(snapshot) != nil {
		return nq.HTTPSMeasurement{}, ErrUnavailable
	}
	return m, err
}
