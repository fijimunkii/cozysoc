package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

// Browser reads expose only normalized historical evidence, never private
// https settings, native prose, scope selectors or execution authority.
type webHTTPSHistory struct {
	Enrolled      bool                 `json:"enrolled"`
	AsOf          time.Time            `json:"as_of"`
	Since         time.Time            `json:"since"`
	Truncated     bool                 `json:"truncated"`
	ScanTruncated bool                 `json:"scan_truncated"`
	Runs          []webHTTPSHistoryRun `json:"runs"`
}
type webHTTPSHistoryRun struct {
	RunID                 string                    `json:"run_id"`
	Selection             api.HTTPSHistorySelection `json:"selection"`
	InterfaceName         string                    `json:"interface_name"`
	InterfaceIndex        int                       `json:"interface_index"`
	LastAuditAt           time.Time                 `json:"last_audit_at"`
	AuthorizationRetained bool                      `json:"authorization_retained"`
	AdmissionRetained     bool                      `json:"admission_retained"`
	TerminalRetained      bool                      `json:"terminal_retained"`
	Outcome               string                    `json:"outcome"`
	Evidence              string                    `json:"evidence"`
	Confidence            string                    `json:"confidence"`
	ExpectationMatched    *bool                     `json:"expectation_matched,omitempty"`
	Measurement           *webHTTPSMeasurement      `json:"measurement,omitempty"`
}
type webHTTPSMeasurement struct {
	StartedAt               time.Time `json:"started_at"`
	CompletedAt             time.Time `json:"completed_at"`
	Exchange                string    `json:"exchange"`
	Request                 string    `json:"request"`
	Stage                   string    `json:"stage"`
	StatusCode              *int      `json:"status_code,omitempty"`
	ResponseTimeNanoseconds *int64    `json:"response_time_ns,omitempty"`
}

func configureWebHTTPSHistory(h *webHandler, stateDir string) {
	h.loadHTTPSHistory = func(ctx context.Context) (api.HTTPSHistory, error) {
		raw, err := localapi.NewClient(stateDir).Call(ctx, api.MethodHTTPSHistory)
		if err != nil {
			return api.HTTPSHistory{}, err
		}
		var history api.HTTPSHistory
		if len(raw) > 64*1024 || json.Unmarshal(raw, &history) != nil {
			return history, httpsrun.ErrHistory
		}
		return history, nil
	}
}
func projectWebHTTPSHistory(native api.HTTPSHistory) (webHTTPSHistory, error) {
	invalid := func() (webHTTPSHistory, error) { return webHTTPSHistory{}, httpsrun.ErrHistory }
	if native.SchemaVersion != 1 || native.Mode != "retained-history" || native.LookupRunID != "" ||
		!webHistoryTime(native.AsOf) || native.Since == nil || !webHistoryTime(*native.Since) ||
		!native.Since.Equal(native.AsOf.Add(-storage.HTTPSHistoryWindow)) ||
		native.Limit != storage.MaxHTTPSHistoryRuns || native.ScanLimit != storage.MaxHTTPSHistoryScan ||
		native.Runs == nil || len(native.Runs) > storage.MaxHTTPSHistoryRuns {
		return invalid()
	}
	if !native.Enrolled && (native.ScopeID != "" || len(native.Runs) != 0 || native.Truncated || native.ScanTruncated) {
		return invalid()
	}
	if native.Enrolled && native.ScopeID == "" {
		return invalid()
	}
	out := webHTTPSHistory{Enrolled: native.Enrolled, AsOf: native.AsOf.UTC(), Since: native.Since.UTC(), Truncated: native.Truncated, ScanTruncated: native.ScanTruncated, Runs: []webHTTPSHistoryRun{}}
	seen := make(map[string]bool)
	for _, r := range native.Runs {
		if seen[r.RunID] || r.Observer.ScopeID != native.ScopeID || !qualityWebInterfacePattern.MatchString(r.Observer.InterfaceName) ||
			!webHistoryTime(r.LastAuditAt) || r.LastAuditAt.Before(*native.Since) || r.LastAuditAt.After(native.AsOf) ||
			(!r.AuthorizationRetained && !r.AdmissionRetained && !r.TerminalRetained) {
			return invalid()
		}
		seen[r.RunID] = true
		// Revalidate only the latest retained phase. Earlier phase times are not in
		// this DTO and must never be reconstructed from the read time.
		e := httpsrun.Event{SchemaVersion: r.AuditSchemaVersion, RunID: r.RunID, Profile: r.Profile, At: r.LastAuditAt,
			Selection: nq.HTTPSSelection{ID: r.Selection.ID, EndpointID: r.Selection.EndpointID, RequestID: r.Selection.RequestID, Family: nq.AddressFamily(r.Selection.Family), Method: r.Selection.Method, ExpectedStatus: r.Selection.ExpectedStatus},
			Observer:  nq.Observer{ScopeID: r.Observer.ScopeID, SensorID: r.Observer.SensorID, InterfaceName: r.Observer.InterfaceName, InterfaceIndex: r.Observer.InterfaceIndex}}
		if r.TerminalRetained {
			e.State, e.Outcome, e.Reason = "finished", r.Outcome, r.Reason
			if r.Measurement != nil {
				m := httpsrun.Measurement(*r.Measurement)
				e.Measurement = &m
			}
		} else {
			if r.Outcome != "unknown" || r.Reason != "" || r.Measurement != nil {
				return invalid()
			}
			e.State = "authorized"
			if r.AdmissionRetained {
				e.State = "admitted"
			}
		}
		derived, err := httpsrun.DescribeRetainedRun([]httpsrun.Event{e}, native.AsOf)
		if err != nil || r.Assessment.State != derived.Assessment.State || r.Assessment.Confidence != derived.Assessment.Confidence ||
			!reflect.DeepEqual(r.Assessment.ExpectationMatched, derived.Assessment.ExpectationMatched) {
			return invalid()
		}
		item := webHTTPSHistoryRun{RunID: r.RunID, Selection: r.Selection, InterfaceName: r.Observer.InterfaceName, InterfaceIndex: r.Observer.InterfaceIndex,
			LastAuditAt: r.LastAuditAt.UTC(), AuthorizationRetained: r.AuthorizationRetained, AdmissionRetained: r.AdmissionRetained, TerminalRetained: r.TerminalRetained,
			Outcome: r.Outcome, Evidence: derived.Assessment.State, Confidence: derived.Assessment.Confidence, ExpectationMatched: derived.Assessment.ExpectationMatched}
		if m := derived.Measurement; m != nil {
			item.Measurement = &webHTTPSMeasurement{StartedAt: m.StartedAt.UTC(), CompletedAt: m.CompletedAt.UTC(), Exchange: string(m.Exchange), Request: string(m.Request), Stage: string(m.Stage), ResponseTimeNanoseconds: m.ResponseTimeNanoseconds}
			if m.StatusCode != 0 {
				code := m.StatusCode
				item.Measurement.StatusCode = &code
			}
		}
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}
func (h *webHandler) handleHTTPSHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "https history") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "https history takes no query or body")
		return
	}
	unavailable := func() {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "retained https history is unavailable")
	}
	if h.loadHTTPSHistory == nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadHTTPSHistory(ctx)
	if err != nil || ctx.Err() != nil {
		unavailable()
		return
	}
	out, err := projectWebHTTPSHistory(native)
	if err != nil {
		unavailable()
		return
	}
	writeWebJSON(w, http.StatusOK, out)
}
