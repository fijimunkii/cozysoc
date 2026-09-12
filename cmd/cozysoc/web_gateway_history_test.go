package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func historyWebFixture(t *testing.T) api.GatewayHistory {
	t.Helper()
	asOf := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	since, end := asOf.Add(-24*time.Hour), asOf.Add(-time.Second)
	zero, loss := int64(0), 0.0
	return api.GatewayHistory{SchemaVersion: 1, Mode: "retained-history", Enrolled: true, ScopeID: "scope.home", AsOf: asOf, Since: &since,
		Limit: storage.MaxGatewayHistoryRuns, ScanLimit: storage.MaxGatewayHistoryScan, Runs: []api.GatewayHistoryRun{{
			RunID: strings.Repeat("a", 32), AuditSchemaVersion: 2, Profile: gatewayrun.Profile, InterfaceName: "en0", InterfaceIndex: 7,
			Target: "192.168.50.1", Source: "192.168.50.23", LastAuditAt: end, AuthorizationRetained: true, AdmissionRetained: true, TerminalRetained: true, Outcome: "completed",
			Measurement: &api.GatewayRunMeasurement{StartedAt: end.Add(-3 * time.Second), CompletedAt: &end, SendCalls: 3, AcceptedRequests: 3, Replies: 3, Complete: true, MeanRTTNanoseconds: &zero},
			Assessment:  api.GatewayHistoricalAssessment{State: "all-replied", Confidence: "limited", ReplyLossPercent: &loss},
		}}}
}

func TestWebGatewayHistoryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method, suffix, body, host, origin string
		auth                                     bool
		want                                     int
	}{
		{"read", "GET", "", "", "", "", true, 200},
		{"no session", "GET", "", "", "", "", false, 401},
		{"post", "POST", "", "", "", "", true, 405},
		{"head", "HEAD", "", "", "", "", true, 405},
		{"query", "GET", "?interface_name=other", "", "", "", true, 400},
		{"empty query", "GET", "?", "", "", "", true, 400},
		{"body", "GET", "", "{}", "", "", true, 400},
		{"host", "GET", "", "", "evil.test", "", true, 403},
		{"origin", "GET", "", "", "", "https://evil.test", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
			h.loadGatewayHistory = func(ctx context.Context) (api.GatewayHistory, error) {
				calls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > webRequestTimeout {
					t.Error("unbounded read")
				}
				return historyWebFixture(t), nil
			}
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000/api/network-quality/history"+tc.suffix, strings.NewReader(tc.body))
			if tc.auth {
				r.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
			}
			if tc.host != "" {
				r.Host = tc.host
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if (calls == 1) != (tc.want == 200) {
				t.Fatalf("invalid request reached loader: %d calls", calls)
			}
			if tc.want == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("sample may be cached")
			}
		})
	}
}

func TestWebGatewayHistoryUnavailableNeverLeaksErrors(t *testing.T) {
	for _, mode := range []string{"missing", "error", "invalid", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
			if mode != "missing" {
				h.loadGatewayHistory = func(context.Context) (api.GatewayHistory, error) {
					if mode == "error" {
						return api.GatewayHistory{}, errors.New("private-path secret")
					}
					return api.GatewayHistory{}, nil
				}
			}
			r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/network-quality/history", nil)
			r.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "session"})
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("unsafe error: %s", w.Body.String())
			}
		})
	}
}

func TestWebHistoryProjectionMinimizesAndCopies(t *testing.T) {
	native := historyWebFixture(t)
	native.Limitations = []string{"private-detail"}
	native.Runs[0].Assessment.Summary, native.Runs[0].Assessment.NextStep = "private-guidance", "private-path"
	native.Runs[0].Reason = "private-diagnostic"
	result, err := projectWebGatewayHistory(native)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	for _, forbidden := range []string{"private-", "scope_id", "profile", "audit_schema_version", "summary", "next_step", "limitations", "reason", "challenge", "ticket", "secret", "reply_loss_percent"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	*native.Runs[0].Measurement.MeanRTTNanoseconds = 42
	*native.Runs[0].Measurement.CompletedAt = native.AsOf
	if *result.Runs[0].Measurement.MeanRTTNanoseconds != 0 || result.Runs[0].Measurement.CompletedAt.Equal(native.AsOf) {
		t.Fatal("projection aliases native measurement")
	}
}

func TestWebHistoryRejectsContradictions(t *testing.T) {
	for name, edit := range map[string]func(*api.GatewayHistory){
		"schema":              func(v *api.GatewayHistory) { v.SchemaVersion = 2 },
		"mode":                func(v *api.GatewayHistory) { v.Mode = "live" },
		"exact lookup":        func(v *api.GatewayHistory) { v.LookupRunID = v.Runs[0].RunID },
		"missing time":        func(v *api.GatewayHistory) { v.AsOf = time.Time{} },
		"window":              func(v *api.GatewayHistory) { v.Since = &v.AsOf },
		"unbounded":           func(v *api.GatewayHistory) { v.Limit = 100 },
		"unbounded scan":      func(v *api.GatewayHistory) { v.ScanLimit = 1000 },
		"no array":            func(v *api.GatewayHistory) { v.Runs = nil },
		"no enrollment":       func(v *api.GatewayHistory) { v.Enrolled = false },
		"duplicate":           func(v *api.GatewayHistory) { v.Runs = append(v.Runs, v.Runs[0]) },
		"bad id":              func(v *api.GatewayHistory) { v.Runs[0].RunID = "secret" },
		"gateway verified":    func(v *api.GatewayHistory) { v.Runs[0].GatewayRoleVerified = true },
		"hostile interface":   func(v *api.GatewayHistory) { v.Runs[0].InterfaceName = "<img>" },
		"public target":       func(v *api.GatewayHistory) { v.Runs[0].Target = "8.8.8.8" },
		"URL source":          func(v *api.GatewayHistory) { v.Runs[0].Source = "https://example.test" },
		"same address":        func(v *api.GatewayHistory) { v.Runs[0].Source = v.Runs[0].Target },
		"future audit":        func(v *api.GatewayHistory) { v.Runs[0].LastAuditAt = v.AsOf.Add(time.Second) },
		"outside window":      func(v *api.GatewayHistory) { v.Runs[0].LastAuditAt = v.Since.Add(-time.Second) },
		"missing terminal":    func(v *api.GatewayHistory) { v.Runs[0].TerminalRetained = false },
		"legacy sample":       func(v *api.GatewayHistory) { v.Runs[0].AuditSchemaVersion = 1 },
		"outcome":             func(v *api.GatewayHistory) { v.Runs[0].Outcome = "internet-up" },
		"blocked sample":      func(v *api.GatewayHistory) { v.Runs[0].Outcome = "blocked" },
		"failed complete":     func(v *api.GatewayHistory) { v.Runs[0].Outcome = "failed" },
		"completed absent":    func(v *api.GatewayHistory) { v.Runs[0].Measurement = nil },
		"too many sends":      func(v *api.GatewayHistory) { v.Runs[0].Measurement.SendCalls = 4 },
		"unaccepted replies":  func(v *api.GatewayHistory) { v.Runs[0].Measurement.AcceptedRequests = 2 },
		"missing RTT":         func(v *api.GatewayHistory) { v.Runs[0].Measurement.MeanRTTNanoseconds = nil },
		"wrong assessment":    func(v *api.GatewayHistory) { v.Runs[0].Assessment.State = "no-replies" },
		"inflated confidence": func(v *api.GatewayHistory) { v.Runs[0].Assessment.Confidence = "high" },
		"wrong loss":          func(v *api.GatewayHistory) { *v.Runs[0].Assessment.ReplyLossPercent = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			v := historyWebFixture(t)
			edit(&v)
			if _, err := projectWebGatewayHistory(v); err == nil {
				t.Fatal("accepted invalid history")
			}
		})
	}
}

func TestWebHistoryKeepsMissingPartialAndCompleteDistinct(t *testing.T) {
	for _, evidence := range []string{"missing-terminal", "execution-only", "no-measurement", "incomplete", "all-replied", "some-replies", "no-replies"} {
		t.Run(evidence, func(t *testing.T) {
			v := historyWebFixture(t)
			r := &v.Runs[0]
			if evidence == "missing-terminal" || evidence == "execution-only" || evidence == "no-measurement" {
				r.Measurement = nil
				r.Assessment = api.GatewayHistoricalAssessment{State: "unknown", Confidence: "unknown"}
				switch evidence {
				case "missing-terminal":
					r.TerminalRetained = false
					r.Outcome = "unknown"
				case "execution-only":
					r.AuditSchemaVersion = 1
				case "no-measurement":
					r.Outcome = "failed"
				}
			} else if evidence == "incomplete" {
				r.Outcome = "canceled"
				r.Measurement.Complete = false
				r.Measurement.CompletedAt = nil
				r.Measurement.MeanRTTNanoseconds = nil
				r.Measurement.SendCalls = 1
				r.Measurement.AcceptedRequests = 1
				r.Measurement.Replies = 0
				r.Measurement.Timeouts = 0
				r.Assessment = api.GatewayHistoricalAssessment{State: "incomplete", Confidence: "unknown"}
			} else if evidence != "all-replied" {
				r.Measurement.Replies = 1
				if evidence == "no-replies" {
					r.Measurement.Replies = 0
					r.Measurement.MeanRTTNanoseconds = nil
				}
				r.Measurement.Timeouts = 3 - r.Measurement.Replies
				loss := 100 * float64(r.Measurement.Timeouts) / 3
				r.Assessment.ReplyLossPercent = &loss
				r.Assessment.State = evidence
			}
			got, err := projectWebGatewayHistory(v)
			if err != nil || got.Runs[0].Evidence != evidence {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	v := historyWebFixture(t)
	v.Enrolled = false
	v.ScopeID = ""
	v.Runs = []api.GatewayHistoryRun{}
	if got, err := projectWebGatewayHistory(v); err != nil || got.Enrolled || len(got.Runs) != 0 {
		t.Fatal("invalid empty history", err)
	}
}
