package gatewayrun

import (
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
)

// Measurement is the bounded terminal audit attachment. Provenance (scope,
// interface, target, source and profile) is carried by the enclosing Event.
// Absent is not a zero-loss sample. Incomplete counts never imply packet loss.
// Durations use explicit nanosecond units; optional measured zero remains zero.
type Measurement struct {
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	SendCalls          int        `json:"send_calls"`
	AcceptedRequests   int        `json:"accepted_requests"`
	Replies            int        `json:"replies"`
	Timeouts           int        `json:"timeouts"`
	Complete           bool       `json:"complete"`
	MeanRTTNanoseconds *int64     `json:"mean_rtt_ns,omitempty"`
}

func copySample(s *gatewayicmp.Sample) *gatewayicmp.Sample {
	if s == nil {
		return nil
	}
	copy := *s
	if s.MeanRTT != nil {
		mean := *s.MeanRTT
		copy.MeanRTT = &mean
	}
	return &copy
}

func auditMeasurement(s *gatewayicmp.Sample) *Measurement {
	if s == nil {
		return nil
	}
	m := &Measurement{StartedAt: s.StartedAt.Round(0).UTC(), SendCalls: s.SendCalls,
		AcceptedRequests: s.AcceptedRequests, Replies: s.Replies, Timeouts: s.Timeouts, Complete: s.Complete}
	if !s.CompletedAt.IsZero() {
		at := s.CompletedAt.Round(0).UTC()
		m.CompletedAt = &at
	}
	if s.MeanRTT != nil {
		ns := int64(*s.MeanRTT)
		m.MeanRTTNanoseconds = &ns
	}
	return m
}

// validateMeasurement checks the fixed v1 traffic profile's structural limits.
// These bounds do not authenticate a sender or certify actual packet spacing.
func validateMeasurement(m Measurement, at time.Time) error {
	if !validTime(at) || !validTime(m.StartedAt) || m.StartedAt.After(at) ||
		m.SendCalls < 0 || m.SendCalls > 3 || m.AcceptedRequests < 0 || m.AcceptedRequests > m.SendCalls ||
		m.SendCalls-m.AcceptedRequests > 1 || m.Replies < 0 || m.Replies > 3 || m.Timeouts < 0 || m.Timeouts > 3 {
		return ErrExecution
	}
	finished := m.Replies + m.Timeouts // Both operands are now bounded.
	if finished > m.AcceptedRequests || m.AcceptedRequests-finished > 1 ||
		(m.SendCalls > m.AcceptedRequests && finished != m.AcceptedRequests) {
		return ErrExecution
	}
	if m.Complete && (m.SendCalls != 3 || m.AcceptedRequests != 3 || finished != 3 || m.CompletedAt == nil) {
		return ErrExecution
	}
	end := at
	if m.CompletedAt != nil {
		end = *m.CompletedAt
		// A sender may finish all windows but fail socket cleanup. It retains its
		// completion timestamp and counts while clearing Complete and MeanRTT.
		if !validTime(end) || end.Before(m.StartedAt) || end.After(at) || finished != 3 ||
			end.Sub(m.StartedAt) >= OperationTimeout {
			return ErrExecution
		}
	}
	// At least two paced intervals for a three-send sample; every completed
	// timeout consumes one second. No multiplication uses unbounded input.
	minimum := max(time.Duration(max(0, m.SendCalls-1))*time.Second, time.Duration(m.Timeouts)*time.Second)
	if end.Sub(m.StartedAt) < minimum {
		return ErrExecution
	}
	if !m.Complete {
		if m.MeanRTTNanoseconds != nil {
			return ErrExecution
		}
	} else if (m.MeanRTTNanoseconds != nil) != (m.Replies > 0) {
		return ErrExecution
	}
	if m.MeanRTTNanoseconds != nil {
		ns := *m.MeanRTTNanoseconds
		if ns < 0 || ns >= int64(time.Second) || ns > int64(end.Sub(m.StartedAt)) {
			return ErrExecution
		}
	}
	return nil
}

// validateSample binds the returned evidence to the exact admitted selection,
// execution window and original review, not merely a newer preflight's expiry.
// It returns a defensive copy or no publishable measurement on invalid evidence.
func validateSample(s gatewayicmp.Sample, executionErr error, selection Selection, started, returned, expires time.Time) (*gatewayicmp.Sample, error) {
	if s == (gatewayicmp.Sample{}) {
		if executionErr != nil {
			return nil, nil
		}
		return nil, ErrExecution
	}
	b := selection.Plan.Binding
	if s.ScopeID != b.ScopeID || s.InterfaceName != b.InterfaceName || s.InterfaceIndex != b.InterfaceIndex ||
		s.Target != selection.Plan.Target || s.Source != selection.Source || s.StartedAt.Before(started) ||
		s.StartedAt.Before(selection.RouteObservedAt) || !s.StartedAt.Before(expires) ||
		(!s.CompletedAt.IsZero() && !s.CompletedAt.Before(expires)) ||
		s.Complete != (executionErr == nil) || validateMeasurement(*auditMeasurement(&s), returned) != nil {
		return nil, ErrExecution
	}
	return copySample(&s), nil
}

// ValidateMeasurement checks the bounded measurement representation at a supplied
// observation time. It proves neither provenance nor consent. Control.Run still
// performs the separate admitted-selection and original-review validation.
func ValidateMeasurement(m Measurement, at time.Time) error {
	return validateMeasurement(m, at)
}
