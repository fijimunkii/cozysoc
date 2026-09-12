package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

type webQualityDiagnosis struct {
	Enrolled      bool                            `json:"enrolled"`
	ReadAt        time.Time                       `json:"read_at"`
	Since         time.Time                       `json:"since"`
	Truncated     bool                            `json:"truncated"`
	ScanTruncated bool                            `json:"scan_truncated"`
	AssessmentAt  *time.Time                      `json:"assessment_at,omitempty"`
	Selected      []webDiagnosisRun               `json:"selected"`
	Compared      []api.QualityDiagnosisReference `json:"compared"`
	EvidenceStart *time.Time                      `json:"evidence_start,omitempty"`
	EvidenceEnd   *time.Time                      `json:"evidence_end,omitempty"`
	Conclusion    string                          `json:"conclusion"`
	Confidence    string                          `json:"confidence"`
}
type webDiagnosisRun struct {
	HTTPS            *webHTTPSHistoryRun           `json:"https,omitempty"`
	Kind             string                        `json:"kind"`
	RunID            string                        `json:"run_id"`
	InterfaceName    string                        `json:"interface_name"`
	InterfaceIndex   int                           `json:"interface_index"`
	LastAuditAt      time.Time                     `json:"last_audit_at"`
	ExecutionOutcome string                        `json:"execution_outcome"`
	SampleStatus     string                        `json:"sample_status"`
	StartedAt        *time.Time                    `json:"started_at,omitempty"`
	CompletedAt      *time.Time                    `json:"completed_at,omitempty"`
	Selection        *api.ResolverHistorySelection `json:"selection,omitempty"`
}

var errWebDiagnosis = errors.New("historical diagnosis is unavailable")

func configureWebQualityDiagnosis(h *webHandler, dir string) {
	h.loadQualityDiagnosis = func(ctx context.Context) (api.QualityDiagnosis, error) {
		raw, err := localapi.NewClient(dir).Call(ctx, api.MethodQualityDiagnosis)
		if err != nil {
			return api.QualityDiagnosis{}, err
		}
		var out api.QualityDiagnosis
		if len(raw) > 65536 || json.Unmarshal(raw, &out) != nil {
			return api.QualityDiagnosis{}, errWebDiagnosis
		}
		return out, nil
	}
}
func diagnosisTimeCopy(at *time.Time) *time.Time {
	if at == nil {
		return nil
	}
	copy := at.UTC()
	return &copy
}

// Check the typed controller interpretation and its context, not a new collection
// or recomputation of raw DNS/ICMP measurements. HTTPS detail is validated by
// the retained-history boundary. Native prose is not published.
func projectWebQualityDiagnosis(v api.QualityDiagnosis) (webQualityDiagnosis, error) {
	invalid := func() (webQualityDiagnosis, error) { return webQualityDiagnosis{}, errWebDiagnosis }
	if v.SchemaVersion != 2 || v.Mode != "retained-comparison" || !webHistoryTime(v.ReadAt) || !webHistoryTime(v.Since) || !v.Since.Equal(v.ReadAt.Add(-24*time.Hour)) ||
		v.RunLimitPerLayer != 20 || v.ScanLimitPerLayer != 256 || v.MaxCompletionSkewMS != 30000 || v.FreshnessMS != 30000 || v.Selected == nil || len(v.Selected) > 3 || v.Compared == nil || len(v.Compared) > 3 {
		return invalid()
	}
	if v.Enrolled != (v.ScopeID != "") || (!v.Enrolled && (len(v.Selected) > 0 || v.Truncated || v.ScanTruncated)) {
		return invalid()
	}
	out := webQualityDiagnosis{Enrolled: v.Enrolled, ReadAt: v.ReadAt.UTC(), Since: v.Since.UTC(), Truncated: v.Truncated, ScanTruncated: v.ScanTruncated, AssessmentAt: diagnosisTimeCopy(v.AssessmentAt),
		Selected: []webDiagnosisRun{}, Compared: []api.QualityDiagnosisReference{}, EvidenceStart: diagnosisTimeCopy(v.EvidenceStart), EvidenceEnd: diagnosisTimeCopy(v.EvidenceEnd), Conclusion: v.Conclusion, Confidence: v.Confidence}
	selected := map[string]api.QualityDiagnosisRun{}
	var anchor, start, end time.Time
	for _, r := range v.Selected {
		if (r.Kind != "gateway" && r.Kind != "resolver" && r.Kind != "https") || selected[r.Kind].Kind != "" || !resolverrun.ValidRunID(r.RunID) || !qualityWebInterfacePattern.MatchString(r.InterfaceName) || r.InterfaceIndex < 1 || r.InterfaceIndex > 2147483647 ||
			!webHistoryTime(r.LastAuditAt) || r.LastAuditAt.Before(v.Since) || r.LastAuditAt.After(v.ReadAt) {
			return invalid()
		}
		switch r.ExecutionOutcome {
		case "unknown", "completed", "blocked", "canceled", "failed", "indeterminate":
		default:
			return invalid()
		}
		if (r.SampleStatus == "missing-terminal") != (r.ExecutionOutcome == "unknown") {
			return invalid()
		}
		switch r.SampleStatus {
		case "missing-terminal", "no-measurement":
			if r.StartedAt != nil || r.CompletedAt != nil {
				return invalid()
			}
		case "incomplete", "recorded":
			if r.StartedAt == nil || !webHistoryTime(*r.StartedAt) || r.StartedAt.After(r.LastAuditAt) || (r.ExecutionOutcome != "completed" && r.ExecutionOutcome != "failed" && r.ExecutionOutcome != "canceled") {
				return invalid()
			}
			if r.SampleStatus == "incomplete" && r.ExecutionOutcome == "completed" {
				return invalid()
			}
			if r.CompletedAt != nil && (!webHistoryTime(*r.CompletedAt) || r.CompletedAt.Before(*r.StartedAt) || r.CompletedAt.After(r.LastAuditAt)) {
				return invalid()
			}
			if (r.SampleStatus == "recorded" || r.Kind != "gateway") && r.CompletedAt == nil {
				return invalid()
			}
			if r.Kind != "https" && r.CompletedAt != nil && r.CompletedAt.Sub(*r.StartedAt) >= 5*time.Second {
				return invalid()
			}
			if r.Kind == "gateway" && r.SampleStatus == "recorded" && (r.ExecutionOutcome == "failed" || r.CompletedAt.Sub(*r.StartedAt) < 2*time.Second) {
				return invalid()
			}
		default:
			return invalid()
		}
		if r.Kind == "resolver" {
			s := r.Selection
			if s == nil || s.Transport != "udp" || nq.ValidateResolverSelection(nq.ResolverSelection{ID: s.ID, ResolverID: s.ResolverID, QueryID: s.QueryID, Family: nq.AddressFamily(s.Family), Transport: nq.DNSTransport(s.Transport), QueryType: nq.DNSQueryType(s.QueryType), Expect: nq.DNSExpectation(s.Expect)}) != nil || (r.ExecutionOutcome == "completed" && r.SampleStatus != "recorded") {
				return invalid()
			}
		} else if r.Selection != nil {
			return invalid()
		}
		var https *webHTTPSHistoryRun
		if r.Kind == "https" {
			if r.HTTPS == nil {
				return invalid()
			}
			projected, err := projectWebHTTPSHistory(api.HTTPSHistory{SchemaVersion: 1, Mode: "retained-history", Enrolled: v.Enrolled, ScopeID: v.ScopeID, AsOf: v.ReadAt, Since: &v.Since, Limit: 20, ScanLimit: 256, Runs: []api.HTTPSHistoryRun{*r.HTTPS}})
			if err != nil {
				return invalid()
			}
			https = &projected.Runs[0]
			status := "no-measurement"
			if !https.TerminalRetained {
				status = "missing-terminal"
			} else if m := https.Measurement; m != nil {
				status = "recorded"
				if m.Exchange == "incomplete" || m.Exchange == "not-measured" {
					status = "incomplete"
				}
				if r.StartedAt == nil || r.CompletedAt == nil || !r.StartedAt.Equal(m.StartedAt) || !r.CompletedAt.Equal(m.CompletedAt) {
					return invalid()
				}
			}
			if r.RunID != https.RunID || r.InterfaceName != https.InterfaceName || r.InterfaceIndex != https.InterfaceIndex || !r.LastAuditAt.Equal(https.LastAuditAt) || r.ExecutionOutcome != https.Outcome || r.SampleStatus != status {
				return invalid()
			}
		} else if r.HTTPS != nil {
			return invalid()
		}
		selected[r.Kind] = r
		if r.LastAuditAt.After(anchor) {
			anchor = r.LastAuditAt
		}
		if r.StartedAt != nil && (start.IsZero() || r.StartedAt.Before(start)) {
			start = *r.StartedAt
		}
		if r.CompletedAt != nil && r.CompletedAt.After(end) {
			end = *r.CompletedAt
		}
		item := webDiagnosisRun{HTTPS: https, Kind: r.Kind, RunID: r.RunID, InterfaceName: r.InterfaceName, InterfaceIndex: r.InterfaceIndex, LastAuditAt: r.LastAuditAt.UTC(), ExecutionOutcome: r.ExecutionOutcome, SampleStatus: r.SampleStatus, StartedAt: diagnosisTimeCopy(r.StartedAt), CompletedAt: diagnosisTimeCopy(r.CompletedAt)}
		if r.Selection != nil {
			copy := *r.Selection
			item.Selection = &copy
		}
		out.Selected = append(out.Selected, item)
	}
	if (len(v.Selected) > 0) != (v.AssessmentAt != nil) || (v.AssessmentAt != nil && (!webHistoryTime(*v.AssessmentAt) || !v.AssessmentAt.Equal(anchor))) {
		return invalid()
	}
	expected := ""
	switch {
	case !v.Enrolled:
		expected = "not-enrolled"
	case v.Truncated || v.ScanTruncated:
		expected = "history-incomplete"
	default:
		expected = qualityDiagnosisPrecondition(v.Selected, anchor)
	}
	if expected != "" {
		if v.Conclusion != expected || v.Confidence != "unknown" || len(v.Compared) != 0 || v.EvidenceStart != nil || v.EvidenceEnd != nil {
			return invalid()
		}
	} else {
		if (v.Conclusion == "external-check-issue-with-responses" && selected["https"].Kind == "") || (v.Conclusion == "dns-query-issue-with-responses" && selected["resolver"].Kind == "") || (v.Conclusion == "icmp-misses-with-responses" && selected["gateway"].Kind == "") {
			return invalid()
		}
		switch v.Conclusion {
		case "dns-query-issue-with-responses", "icmp-misses-with-responses", "problems-across-selected-layers", "selected-checks-matched", "mixed-or-limited-evidence", "external-check-issue-with-responses":
		default:
			return invalid()
		}
		if v.Confidence != "limited" || len(v.Compared) != len(v.Selected) || v.EvidenceStart == nil || v.EvidenceEnd == nil || !v.EvidenceStart.Equal(start) || !v.EvidenceEnd.Equal(end) {
			return invalid()
		}
		seen := map[string]bool{}
		for _, r := range v.Compared {
			if seen[r.Kind] || selected[r.Kind].RunID != r.RunID || selected[r.Kind].Kind == "" {
				return invalid()
			}
			seen[r.Kind] = true
			out.Compared = append(out.Compared, r)
		}
	}
	return out, nil
}
func (h *webHandler) handleQualityDiagnosis(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "quality diagnosis") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "quality diagnosis takes no query or body")
		return
	}
	unavailable := func() {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "historical quality diagnosis is unavailable")
	}
	if h.loadQualityDiagnosis == nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadQualityDiagnosis(ctx)
	if err != nil || ctx.Err() != nil {
		unavailable()
		return
	}
	out, err := projectWebQualityDiagnosis(native)
	if err != nil {
		unavailable()
		return
	}
	writeWebJSON(w, http.StatusOK, out)
}
