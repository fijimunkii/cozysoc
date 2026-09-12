package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func httpsHistoryWebFixture(t *testing.T) api.HTTPSHistory {
	t.Helper()
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	since, end, zero, matched := at.Add(-24*time.Hour), at.Add(-time.Second), int64(0), true
	return api.HTTPSHistory{SchemaVersion: 1, Mode: "retained-history", Enrolled: true, ScopeID: "scope.home", AsOf: at, Since: &since, Limit: storage.MaxHTTPSHistoryRuns, ScanLimit: storage.MaxHTTPSHistoryScan,
		Runs: []api.HTTPSHistoryRun{{RunID: strings.Repeat("a", 32), AuditSchemaVersion: 1, Profile: httpsrun.Profile,
			Selection: api.HTTPSHistorySelection{ID: "selection.test", EndpointID: "endpoint.test", RequestID: "request.test", Family: "ipv4", Method: "HEAD", ExpectedStatus: 204},
			Observer:  api.HTTPSHistoryObserver{ScopeID: "scope.home", SensorID: "sensor.test", InterfaceName: "en0", InterfaceIndex: 7}, LastAuditAt: end, AuthorizationRetained: true, AdmissionRetained: true, TerminalRetained: true, Outcome: "completed",
			Measurement: &api.HTTPSRunMeasurement{StartedAt: end.Add(-time.Second), CompletedAt: end, Exchange: nq.HTTPSResponseReceived, Request: nq.HTTPSRequestAccepted, Stage: nq.HTTPSRequest, StatusCode: 204, ResponseTimeNanoseconds: &zero},
			Assessment:  api.HTTPSHistoricalAssessment{State: "status-response", Confidence: "limited", ExpectationMatched: &matched}}}}
}
func TestWebHTTPSHistoryBoundary(t *testing.T) {
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
			h.loadHTTPSHistory = func(ctx context.Context) (api.HTTPSHistory, error) {
				calls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > webRequestTimeout {
					t.Error("unbounded read")
				}
				return httpsHistoryWebFixture(t), nil
			}
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000/api/network-quality/https-history"+tc.suffix, strings.NewReader(tc.body))
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

func TestWebHTTPSHistoryUnavailableNeverLeaksErrors(t *testing.T) {
	for _, mode := range []string{"missing", "error", "invalid", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
			if mode != "missing" {
				h.loadHTTPSHistory = func(context.Context) (api.HTTPSHistory, error) {
					if mode == "error" {
						return api.HTTPSHistory{}, errors.New("private-path secret")
					}
					return api.HTTPSHistory{}, nil
				}
			}
			r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/network-quality/https-history", nil)
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

func TestWebHTTPSProjectionValidatesAndOwnsEvidence(t *testing.T) {
	v := httpsHistoryWebFixture(t)
	v.Limitations = []string{"private-note"}
	v.Runs[0].Assessment.Summary = "private-summary"
	v.Runs[0].Assessment.NextStep = "private-next"
	out, err := projectWebHTTPSHistory(v)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	for _, forbidden := range []string{"private-", "scope_id", "sensor_id", "reason", "profile", `"endpoint":`, "server_name", "request_target", "challenge", "ticket"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	*v.Runs[0].Measurement.ResponseTimeNanoseconds = 1
	*v.Runs[0].Assessment.ExpectationMatched = false
	v.Runs[0].Measurement.StatusCode = 5
	if *out.Runs[0].Measurement.ResponseTimeNanoseconds != 0 || !*out.Runs[0].ExpectationMatched || *out.Runs[0].Measurement.StatusCode != 204 {
		t.Fatal("aliased native evidence")
	}
	for name, edit := range map[string]func(*api.HTTPSHistory){
		"schema":           func(v *api.HTTPSHistory) { v.SchemaVersion = 2 },
		"exact lookup":     func(v *api.HTTPSHistory) { v.LookupRunID = v.Runs[0].RunID },
		"window":           func(v *api.HTTPSHistory) { v.Since = &v.AsOf },
		"limit":            func(v *api.HTTPSHistory) { v.Limit = 21 },
		"scan limit":       func(v *api.HTTPSHistory) { v.ScanLimit = 257 },
		"nil runs":         func(v *api.HTTPSHistory) { v.Runs = nil },
		"enrollment":       func(v *api.HTTPSHistory) { v.Enrolled = false },
		"scope":            func(v *api.HTTPSHistory) { v.Runs[0].Observer.ScopeID = "scope.other" },
		"duplicate":        func(v *api.HTTPSHistory) { v.Runs = append(v.Runs, v.Runs[0]) },
		"interface":        func(v *api.HTTPSHistory) { v.Runs[0].Observer.InterfaceName = "<img>" },
		"future":           func(v *api.HTTPSHistory) { v.Runs[0].LastAuditAt = v.AsOf.Add(time.Second) },
		"old":              func(v *api.HTTPSHistory) { v.Runs[0].LastAuditAt = v.Since.Add(-time.Second) },
		"missing terminal": func(v *api.HTTPSHistory) { v.Runs[0].TerminalRetained = false },
		"unknown version":  func(v *api.HTTPSHistory) { v.Runs[0].AuditSchemaVersion = 2 },
		"completed absent": func(v *api.HTTPSHistory) { v.Runs[0].Measurement = nil },
		"blocked sample": func(v *api.HTTPSHistory) {
			v.Runs[0].Outcome = "blocked"
			v.Runs[0].Reason = "preflight-unavailable"
		},
		"missing timing":   func(v *api.HTTPSHistory) { v.Runs[0].Measurement.ResponseTimeNanoseconds = nil },
		"oversized timing": func(v *api.HTTPSHistory) { *v.Runs[0].Measurement.ResponseTimeNanoseconds = int64(2 * time.Second) },
		"wrong result":     func(v *api.HTTPSHistory) { v.Runs[0].Assessment.State = "timeout" },
		"confidence":       func(v *api.HTTPSHistory) { v.Runs[0].Assessment.Confidence = "high" },
		"expectation":      func(v *api.HTTPSHistory) { *v.Runs[0].Assessment.ExpectationMatched = false },
		"status":           func(v *api.HTTPSHistory) { v.Runs[0].Measurement.StatusCode = 16 },
	} {
		t.Run(name, func(t *testing.T) {
			v := httpsHistoryWebFixture(t)
			edit(&v)
			if _, err := projectWebHTTPSHistory(v); err == nil {
				t.Fatal("accepted invalid history")
			}
		})
	}
}
func TestWebHTTPSProjectionPreservesNegativeAndMissingEvidence(t *testing.T) {
	for _, kind := range []string{"status-response", "unexpected", "timeout", "failed-response", "missing-terminal", "no-measurement", "unenrolled"} {
		t.Run(kind, func(t *testing.T) {
			v := httpsHistoryWebFixture(t)
			r := &v.Runs[0]
			switch kind {
			case "status-response", "unexpected":
				r.Measurement.StatusCode = 503
				r.Assessment.State = "status-response"
				if kind == "status-response" {
					r.Selection.ExpectedStatus = 503
				} else {
					*r.Assessment.ExpectationMatched = false
				}
			case "timeout":
				r.Measurement.Exchange = nq.HTTPSTimeout
				r.Measurement.StatusCode = 0
				r.Measurement.ResponseTimeNanoseconds = nil
				r.Measurement.StartedAt = r.Measurement.CompletedAt.Add(-2 * time.Second)
				r.Assessment.State = "timeout"
				r.Assessment.ExpectationMatched = nil
			case "failed-response":
				r.Outcome = "failed"
				r.Reason = "execution-error"
			case "missing-terminal", "no-measurement":
				r.Measurement = nil
				r.Assessment = api.HTTPSHistoricalAssessment{State: "unknown", Confidence: "unknown"}
				if kind == "missing-terminal" {
					r.TerminalRetained = false
					r.Outcome = "unknown"
				} else {
					r.Outcome = "failed"
					r.Reason = "execution-error"
				}
			case "unenrolled":
				v.Enrolled = false
				v.ScopeID = ""
				v.Runs = []api.HTTPSHistoryRun{}
			}
			out, err := projectWebHTTPSHistory(v)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Runs) > 0 && (out.Runs[0].Evidence != r.Assessment.State || out.Runs[0].Outcome != r.Outcome) {
				t.Fatal("changed historical evidence")
			}
		})
	}
}
