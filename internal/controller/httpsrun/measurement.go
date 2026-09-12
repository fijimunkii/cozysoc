package httpsrun

import (
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// Measurement has explicit units and no raw HTTPS content. Original identity and
// observer are carried by the enclosing event. Absence never means no requests.
type Measurement struct {
	StartedAt               time.Time            `json:"started_at"`
	CompletedAt             time.Time            `json:"completed_at"`
	Exchange                nq.HTTPSExchange     `json:"exchange"`
	Request                 nq.HTTPSRequestState `json:"request"`
	Gap                     nq.GapReason         `json:"gap,omitempty"`
	Stage                   nq.HTTPSStage        `json:"stage"`
	StatusCode              int                  `json:"status_code,omitempty"`
	ResponseTimeNanoseconds *int64               `json:"response_time_ns,omitempty"`
}

func copySample(s *nq.HTTPSMeasurement) *nq.HTTPSMeasurement {
	if s == nil {
		return nil
	}
	m := *s

	if s.ResponseTime != nil {
		d := *s.ResponseTime
		m.ResponseTime = &d
	}
	return &m
}
func auditMeasurement(s *nq.HTTPSMeasurement) *Measurement {
	s = copySample(s)
	if s == nil {
		return nil
	}
	m := &Measurement{StartedAt: s.StartedAt.Round(0).UTC(), CompletedAt: s.CompletedAt.Round(0).UTC(), Exchange: s.Exchange, Request: s.Request, Gap: s.Gap, Stage: s.Stage, StatusCode: s.StatusCode}
	if s.ResponseTime != nil {
		ns := int64(*s.ResponseTime)
		m.ResponseTimeNanoseconds = &ns
	}
	return m
}
func (m Measurement) sample(id string, s nq.HTTPSSelection, o nq.Observer) nq.HTTPSMeasurement {
	result := nq.HTTPSMeasurement{ID: id, Selection: s, Observer: o, StartedAt: m.StartedAt, CompletedAt: m.CompletedAt, Exchange: m.Exchange, Request: m.Request, Gap: m.Gap, Stage: m.Stage, StatusCode: m.StatusCode}
	if m.ResponseTimeNanoseconds != nil {
		d := time.Duration(*m.ResponseTimeNanoseconds)
		result.ResponseTime = &d
	}
	return *copySample(&result)
}
func complete(m nq.HTTPSMeasurement) bool {
	return m.Exchange == nq.HTTPSResponseReceived || m.Exchange == nq.HTTPSTimeout
}
func validateMeasurement(m nq.HTTPSMeasurement, now time.Time) error {
	if !validTime(m.StartedAt) || !validTime(m.CompletedAt) || m.CompletedAt.Sub(m.StartedAt) > nq.MaxCheckTime {
		return ErrExecution
	}
	if m.Exchange == nq.HTTPSResponseReceived && (m.ResponseTime == nil || *m.ResponseTime >= OperationTimeout) {
		return ErrExecution
	}
	snapshot := nq.HTTPSSnapshot{Observer: m.Observer, Selections: []nq.HTTPSSelection{m.Selection}, AsOf: now, WindowStart: m.StartedAt, Freshness: time.Second, Measurements: []nq.HTTPSMeasurement{m}}
	if nq.ValidateHTTPSSnapshot(snapshot) != nil {
		return ErrExecution
	}
	return nil
}
func validateSample(s nq.HTTPSMeasurement, executionErr error, selection Selection, id string, started, returned, expires time.Time) (*nq.HTTPSMeasurement, error) {
	if s == (nq.HTTPSMeasurement{}) {
		if executionErr != nil {
			return nil, nil
		}
		return nil, ErrExecution
	}
	d := selection.Plan.Disclosure()
	if s.ID != id || s.Selection != d.Configuration.Selection || s.Observer != d.Binding.Observer || s.StartedAt.Before(started) ||
		s.StartedAt.Before(selection.RouteObservedAt) || !s.StartedAt.Before(expires) || (complete(s) && !s.CompletedAt.Before(expires)) ||
		(executionErr == nil && !complete(s)) || validateMeasurement(s, returned) != nil {
		return nil, ErrExecution
	}
	return copySample(&s), nil
}
