package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

// Browser reads expose only normalized historical evidence, never private
// resolver settings, native prose, scope selectors or execution authority.
type webResolverHistory struct {
	Enrolled      bool                    `json:"enrolled"`
	AsOf          time.Time               `json:"as_of"`
	Since         time.Time               `json:"since"`
	Truncated     bool                    `json:"truncated"`
	ScanTruncated bool                    `json:"scan_truncated"`
	Runs          []webResolverHistoryRun `json:"runs"`
}
type webResolverHistoryRun struct {
	RunID                 string                       `json:"run_id"`
	Selection             api.ResolverHistorySelection `json:"selection"`
	InterfaceName         string                       `json:"interface_name"`
	InterfaceIndex        int                          `json:"interface_index"`
	LastAuditAt           time.Time                    `json:"last_audit_at"`
	AuthorizationRetained bool                         `json:"authorization_retained"`
	AdmissionRetained     bool                         `json:"admission_retained"`
	TerminalRetained      bool                         `json:"terminal_retained"`
	Outcome               string                       `json:"outcome"`
	Evidence              string                       `json:"evidence"`
	Confidence            string                       `json:"confidence"`
	ExpectationMatched    *bool                        `json:"expectation_matched,omitempty"`
	Measurement           *webResolverMeasurement      `json:"measurement,omitempty"`
}
type webResolverMeasurement struct {
	StartedAt               time.Time `json:"started_at"`
	CompletedAt             time.Time `json:"completed_at"`
	Exchange                string    `json:"exchange"`
	Request                 string    `json:"request"`
	RCode                   *int      `json:"rcode,omitempty"`
	ResponseTimeNanoseconds *int64    `json:"response_time_ns,omitempty"`
}

func configureWebResolverHistory(h *webHandler, stateDir string) {
	h.loadResolverHistory = func(ctx context.Context) (api.ResolverHistory, error) {
		raw, err := localapi.NewClient(stateDir).Call(ctx, api.MethodResolverHistory)
		if err != nil {
			return api.ResolverHistory{}, err
		}
		var history api.ResolverHistory
		if len(raw) > 64*1024 || json.Unmarshal(raw, &history) != nil {
			return history, resolverrun.ErrHistory
		}
		return history, nil
	}
}
func projectWebResolverHistory(native api.ResolverHistory) (webResolverHistory, error) {
	invalid := func() (webResolverHistory, error) { return webResolverHistory{}, resolverrun.ErrHistory }
	if native.SchemaVersion != 1 || native.Mode != "retained-history" || native.LookupRunID != "" ||
		!webHistoryTime(native.AsOf) || native.Since == nil || !webHistoryTime(*native.Since) ||
		!native.Since.Equal(native.AsOf.Add(-storage.ResolverHistoryWindow)) ||
		native.Limit != storage.MaxResolverHistoryRuns || native.ScanLimit != storage.MaxResolverHistoryScan ||
		native.Runs == nil || len(native.Runs) > storage.MaxResolverHistoryRuns {
		return invalid()
	}
	if !native.Enrolled && (native.ScopeID != "" || len(native.Runs) != 0 || native.Truncated || native.ScanTruncated) {
		return invalid()
	}
	if native.Enrolled && native.ScopeID == "" {
		return invalid()
	}
	out := webResolverHistory{Enrolled: native.Enrolled, AsOf: native.AsOf.UTC(), Since: native.Since.UTC(), Truncated: native.Truncated, ScanTruncated: native.ScanTruncated, Runs: []webResolverHistoryRun{}}
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
		e := resolverrun.Event{SchemaVersion: r.AuditSchemaVersion, RunID: r.RunID, Profile: r.Profile, At: r.LastAuditAt,
			Selection: nq.ResolverSelection{ID: r.Selection.ID, ResolverID: r.Selection.ResolverID, QueryID: r.Selection.QueryID, Family: nq.AddressFamily(r.Selection.Family), Transport: nq.DNSTransport(r.Selection.Transport), QueryType: nq.DNSQueryType(r.Selection.QueryType), Expect: nq.DNSExpectation(r.Selection.Expect)},
			Observer:  nq.Observer{ScopeID: r.Observer.ScopeID, SensorID: r.Observer.SensorID, InterfaceName: r.Observer.InterfaceName, InterfaceIndex: r.Observer.InterfaceIndex}}
		if r.TerminalRetained {
			e.State, e.Outcome, e.Reason = "finished", r.Outcome, r.Reason
			if r.Measurement != nil {
				m := resolverrun.Measurement(*r.Measurement)
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
		derived, err := resolverrun.DescribeRetainedRun([]resolverrun.Event{e}, native.AsOf)
		if err != nil || r.Assessment.State != derived.Assessment.State || r.Assessment.Confidence != derived.Assessment.Confidence ||
			!reflect.DeepEqual(r.Assessment.ExpectationMatched, derived.Assessment.ExpectationMatched) {
			return invalid()
		}
		item := webResolverHistoryRun{RunID: r.RunID, Selection: r.Selection, InterfaceName: r.Observer.InterfaceName, InterfaceIndex: r.Observer.InterfaceIndex,
			LastAuditAt: r.LastAuditAt.UTC(), AuthorizationRetained: r.AuthorizationRetained, AdmissionRetained: r.AdmissionRetained, TerminalRetained: r.TerminalRetained,
			Outcome: r.Outcome, Evidence: derived.Assessment.State, Confidence: derived.Assessment.Confidence, ExpectationMatched: derived.Assessment.ExpectationMatched}
		if m := derived.Measurement; m != nil {
			item.Measurement = &webResolverMeasurement{StartedAt: m.StartedAt.UTC(), CompletedAt: m.CompletedAt.UTC(), Exchange: string(m.Exchange), Request: string(m.Request), ResponseTimeNanoseconds: m.ResponseTimeNanoseconds}
			if m.Reply != nil {
				code := m.Reply.RCode
				item.Measurement.RCode = &code
			}
		}
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}
func (h *webHandler) handleResolverHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "resolver history") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "resolver history takes no query or body")
		return
	}
	unavailable := func() {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "retained resolver history is unavailable")
	}
	if h.loadResolverHistory == nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadResolverHistory(ctx)
	if err != nil || ctx.Err() != nil {
		unavailable()
		return
	}
	out, err := projectWebResolverHistory(native)
	if err != nil {
		unavailable()
		return
	}
	writeWebJSON(w, http.StatusOK, out)
}
