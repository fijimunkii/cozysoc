package resolverrun

import (
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"time"
)

// Measurement has explicit units and no raw DNS content. Original identity and
// observer are carried by the enclosing event. Absence never means no requests.
type Measurement struct {
	StartedAt               time.Time           `json:"started_at"`
	CompletedAt             time.Time           `json:"completed_at"`
	Exchange                nq.DNSExchangeState `json:"exchange"`
	Request                 nq.DNSRequestState  `json:"request"`
	Gap                     nq.GapReason        `json:"gap,omitempty"`
	Reply                   *nq.DNSReply        `json:"reply,omitempty"`
	ResponseTimeNanoseconds *int64              `json:"response_time_ns,omitempty"`
}

func copySample(s *nq.ResolverMeasurement) *nq.ResolverMeasurement {
	if s == nil {
		return nil
	}
	m := *s
	if s.Reply != nil {
		r := *s.Reply
		m.Reply = &r
	}
	if s.ResponseTime != nil {
		d := *s.ResponseTime
		m.ResponseTime = &d
	}
	return &m
}
func auditMeasurement(s *nq.ResolverMeasurement) *Measurement {
	s = copySample(s)
	if s == nil {
		return nil
	}
	m := &Measurement{StartedAt: s.StartedAt.Round(0).UTC(), CompletedAt: s.CompletedAt.Round(0).UTC(), Exchange: s.Exchange, Request: s.Request, Gap: s.Gap, Reply: s.Reply}
	if s.ResponseTime != nil {
		ns := int64(*s.ResponseTime)
		m.ResponseTimeNanoseconds = &ns
	}
	return m
}
func (m Measurement) sample(id string, s nq.ResolverSelection, o nq.Observer) nq.ResolverMeasurement {
	result := nq.ResolverMeasurement{ID: id, Selection: s, Observer: o, StartedAt: m.StartedAt, CompletedAt: m.CompletedAt, Exchange: m.Exchange, Request: m.Request, Gap: m.Gap, Reply: m.Reply}
	if m.ResponseTimeNanoseconds != nil {
		d := time.Duration(*m.ResponseTimeNanoseconds)
		result.ResponseTime = &d
	}
	return *copySample(&result)
}
func complete(m nq.ResolverMeasurement) bool {
	return m.Exchange == nq.DNSResponseReceived || m.Exchange == nq.DNSTimeout
}
func validateMeasurement(m nq.ResolverMeasurement, now time.Time) error {
	if !validTime(m.StartedAt) || !validTime(m.CompletedAt) || m.CompletedAt.Sub(m.StartedAt) >= OperationTimeout || m.Selection.Transport != nq.DNSUDP {
		return ErrExecution
	}
	if m.Reply != nil && m.Reply.RCode > 15 {
		return ErrExecution
	} // Classic DNS, no EDNS.
	if m.Exchange == nq.DNSTimeout && m.CompletedAt.Sub(m.StartedAt) < 2*time.Second {
		return ErrExecution
	}
	if m.Exchange == nq.DNSResponseReceived && (m.ResponseTime == nil || *m.ResponseTime >= 2*time.Second) {
		return ErrExecution
	}
	snapshot := nq.ResolverSnapshot{Observer: m.Observer, Selections: []nq.ResolverSelection{m.Selection}, AsOf: now, WindowStart: m.StartedAt, Freshness: time.Second, Measurements: []nq.ResolverMeasurement{m}}
	if nq.ValidateResolverSnapshot(snapshot) != nil {
		return ErrExecution
	}
	return nil
}
func validateSample(s nq.ResolverMeasurement, executionErr error, selection Selection, id string, started, returned, expires time.Time) (*nq.ResolverMeasurement, error) {
	if s == (nq.ResolverMeasurement{}) {
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
