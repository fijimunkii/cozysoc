package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

// A list-only browser projection: no scope selectors, native prose, approval
// material, or execution methods. Every timestamp describes retained evidence.
type webGatewayHistory struct {
	Enrolled      bool                   `json:"enrolled"`
	AsOf          time.Time              `json:"as_of"`
	Since         time.Time              `json:"since"`
	Truncated     bool                   `json:"truncated"`
	ScanTruncated bool                   `json:"scan_truncated"`
	Runs          []webGatewayHistoryRun `json:"runs"`
}

type webGatewayHistoryRun struct {
	RunID                 string                 `json:"run_id"`
	InterfaceName         string                 `json:"interface_name"`
	InterfaceIndex        int                    `json:"interface_index"`
	Target                string                 `json:"target"`
	Source                string                 `json:"source"`
	LastAuditAt           time.Time              `json:"last_audit_at"`
	AuthorizationRetained bool                   `json:"authorization_retained"`
	AdmissionRetained     bool                   `json:"admission_retained"`
	TerminalRetained      bool                   `json:"terminal_retained"`
	Outcome               string                 `json:"outcome"`
	Evidence              string                 `json:"evidence"`
	Confidence            string                 `json:"confidence"`
	Measurement           *webHistoryMeasurement `json:"measurement,omitempty"`
}

type webHistoryMeasurement struct {
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	SendCalls          int        `json:"send_calls"`
	AcceptedRequests   int        `json:"accepted_requests"`
	Replies            int        `json:"replies"`
	Timeouts           int        `json:"timeouts"`
	Complete           bool       `json:"complete"`
	MeanRTTNanoseconds *int64     `json:"mean_rtt_ns,omitempty"`
}

func configureWebGatewayHistory(h *webHandler, stateDir string) {
	h.loadGatewayHistory = func(ctx context.Context) (api.GatewayHistory, error) {
		raw, err := localapi.NewClient(stateDir).Call(ctx, api.MethodGatewayHistory)
		if err != nil {
			return api.GatewayHistory{}, err
		}
		var history api.GatewayHistory
		if len(raw) > 64*1024 || json.Unmarshal(raw, &history) != nil {
			return history, gatewayrun.ErrHistory
		}
		return history, nil
	}
}

func webHistoryTime(at time.Time) bool {
	return !at.IsZero() && time.Unix(0, at.UnixNano()).Equal(at)
}

func projectWebGatewayHistory(native api.GatewayHistory) (webGatewayHistory, error) {
	invalid := func() (webGatewayHistory, error) {
		return webGatewayHistory{}, fmt.Errorf("invalid gateway history projection")
	}
	if native.SchemaVersion != 1 || native.Mode != "retained-history" || native.LookupRunID != "" ||
		!webHistoryTime(native.AsOf) || native.Since == nil || !webHistoryTime(*native.Since) ||
		!native.Since.Equal(native.AsOf.Add(-storage.GatewayHistoryWindow)) ||
		native.Limit != storage.MaxGatewayHistoryRuns || native.ScanLimit != storage.MaxGatewayHistoryScan ||
		native.Runs == nil || len(native.Runs) > storage.MaxGatewayHistoryRuns {
		return invalid()
	}
	if !native.Enrolled && (native.ScopeID != "" || len(native.Runs) != 0 || native.Truncated || native.ScanTruncated) {
		return invalid()
	}
	if native.Enrolled && native.ScopeID == "" {
		return invalid()
	}
	out := webGatewayHistory{Enrolled: native.Enrolled, AsOf: native.AsOf.UTC(), Since: native.Since.UTC(),
		Truncated: native.Truncated, ScanTruncated: native.ScanTruncated, Runs: []webGatewayHistoryRun{}}
	seen := make(map[string]bool)
	for _, r := range native.Runs {
		if !gatewayrun.ValidRunID(r.RunID) || seen[r.RunID] || r.Profile != gatewayrun.Profile || r.GatewayRoleVerified ||
			(r.AuditSchemaVersion != 1 && r.AuditSchemaVersion != 2) ||
			!qualityWebInterfacePattern.MatchString(r.InterfaceName) || r.InterfaceIndex < 1 || r.InterfaceIndex > 2147483647 ||
			networkquality.ValidateGatewayPreviewTarget(r.Target) != nil || networkquality.ValidateGatewayPreviewTarget(r.Source) != nil || r.Source == r.Target ||
			!webHistoryTime(r.LastAuditAt) || r.LastAuditAt.Before(*native.Since) || r.LastAuditAt.After(native.AsOf) ||
			(!r.AuthorizationRetained && !r.AdmissionRetained && !r.TerminalRetained) {
			return invalid()
		}
		seen[r.RunID] = true
		evidence, state, confidence := "missing-terminal", "unknown", "unknown"
		if !r.TerminalRetained {
			if r.Outcome != "unknown" || r.Measurement != nil {
				return invalid()
			}
		} else {
			switch r.Outcome {
			case "completed", "blocked", "canceled", "failed", "indeterminate":
			default:
				return invalid()
			}
			evidence = "no-measurement"
			if r.AuditSchemaVersion == 1 {
				if r.Measurement != nil {
					return invalid()
				}
				evidence = "execution-only"
			} else if r.Outcome == "completed" && (r.Measurement == nil || !r.Measurement.Complete) {
				return invalid()
			}
		}
		item := webGatewayHistoryRun{RunID: r.RunID, InterfaceName: r.InterfaceName, InterfaceIndex: r.InterfaceIndex,
			Target: r.Target, Source: r.Source, LastAuditAt: r.LastAuditAt.UTC(), AuthorizationRetained: r.AuthorizationRetained,
			AdmissionRetained: r.AdmissionRetained, TerminalRetained: r.TerminalRetained, Outcome: r.Outcome}
		if m := r.Measurement; m != nil {
			if gatewayrun.ValidateMeasurement(gatewayrun.Measurement(*m), r.LastAuditAt) != nil ||
				(r.Outcome != "completed" && r.Outcome != "canceled" && r.Outcome != "failed") || (r.Outcome == "failed" && m.Complete) {
				return invalid()
			}
			evidence, state = "incomplete", "incomplete"
			if m.Complete {
				confidence = "limited"
				switch m.Replies {
				case 0:
					evidence = "no-replies"
				case 3:
					evidence = "all-replied"
				default:
					evidence = "some-replies"
				}
				state = evidence
				loss := 100 * float64(m.Timeouts) / float64(m.AcceptedRequests)
				if r.Assessment.ReplyLossPercent == nil || math.IsNaN(*r.Assessment.ReplyLossPercent) || *r.Assessment.ReplyLossPercent != loss {
					return invalid()
				}
			}
			copy := &webHistoryMeasurement{StartedAt: m.StartedAt.UTC(), SendCalls: m.SendCalls, AcceptedRequests: m.AcceptedRequests, Replies: m.Replies, Timeouts: m.Timeouts, Complete: m.Complete}
			if m.CompletedAt != nil {
				at := m.CompletedAt.UTC()
				copy.CompletedAt = &at
			}
			if m.MeanRTTNanoseconds != nil {
				ns := *m.MeanRTTNanoseconds
				copy.MeanRTTNanoseconds = &ns
			}
			item.Measurement = copy
		}
		if r.Assessment.State != state || r.Assessment.Confidence != confidence ||
			(confidence == "unknown" && r.Assessment.ReplyLossPercent != nil) {
			return invalid()
		}
		item.Evidence, item.Confidence = evidence, confidence
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}

func (h *webHandler) handleGatewayHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.authenticated(r) {
		writeWebError(w, http.StatusUnauthorized, "web_session_required", "open the authenticated local Cozy SOC web URL")
		return
	}
	if !validateParameterlessWebRead(w, r, "gateway history") {
		return
	}
	if r.URL.ForceQuery || r.ContentLength < 0 {
		writeWebError(w, http.StatusBadRequest, "invalid_request", "gateway history takes no query or body")
		return
	}
	unavailable := func() {
		writeWebError(w, http.StatusServiceUnavailable, "controller_unavailable", "retained gateway history is unavailable")
	}
	if h.loadGatewayHistory == nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()
	native, err := h.loadGatewayHistory(ctx)
	if err != nil || ctx.Err() != nil {
		unavailable()
		return
	}
	out, err := projectWebGatewayHistory(native)
	if err != nil {
		unavailable()
		return
	}
	writeWebJSON(w, http.StatusOK, out)
}
