package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resolverHistoryWebFixture(t *testing.T) api.ResolverHistory {
	t.Helper()
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	since, end, zero, matched := at.Add(-24*time.Hour), at.Add(-time.Second), int64(0), true
	return api.ResolverHistory{SchemaVersion: 1, Mode: "retained-history", Enrolled: true, ScopeID: "scope.home", AsOf: at, Since: &since, Limit: storage.MaxResolverHistoryRuns, ScanLimit: storage.MaxResolverHistoryScan,
		Runs: []api.ResolverHistoryRun{{RunID: strings.Repeat("a", 32), AuditSchemaVersion: 1, Profile: resolverrun.Profile,
			Selection: api.ResolverHistorySelection{ID: "selection.test", ResolverID: "resolver.test", QueryID: "query.test", Family: "ipv4", Transport: "udp", QueryType: "AAAA", Expect: "answer"},
			Observer:  api.ResolverHistoryObserver{ScopeID: "scope.home", SensorID: "sensor.test", InterfaceName: "en0", InterfaceIndex: 7}, LastAuditAt: end, AuthorizationRetained: true, AdmissionRetained: true, TerminalRetained: true, Outcome: "completed",
			Measurement: &api.ResolverRunMeasurement{StartedAt: end.Add(-time.Second), CompletedAt: end, Exchange: nq.DNSResponseReceived, Request: nq.DNSRequestAccepted, Reply: &nq.DNSReply{Answer: nq.DNSAnswerPresent}, ResponseTimeNanoseconds: &zero},
			Assessment:  api.ResolverHistoricalAssessment{State: "answer", Confidence: "limited", ExpectationMatched: &matched}}}}
}
func TestWebResolverHistoryBoundary(t *testing.T) {
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
			h.loadResolverHistory = func(ctx context.Context) (api.ResolverHistory, error) {
				calls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > webRequestTimeout {
					t.Error("unbounded read")
				}
				return resolverHistoryWebFixture(t), nil
			}
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000/api/network-quality/resolver-history"+tc.suffix, strings.NewReader(tc.body))
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

func TestWebResolverHistoryUnavailableNeverLeaksErrors(t *testing.T) {
	for _, mode := range []string{"missing", "error", "invalid", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
			if mode != "missing" {
				h.loadResolverHistory = func(context.Context) (api.ResolverHistory, error) {
					if mode == "error" {
						return api.ResolverHistory{}, errors.New("private-path secret")
					}
					return api.ResolverHistory{}, nil
				}
			}
			r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/network-quality/resolver-history", nil)
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

func TestWebResolverProjectionValidatesAndOwnsEvidence(t *testing.T) {
	v := resolverHistoryWebFixture(t)
	v.Limitations = []string{"private-note"}
	v.Runs[0].Assessment.Summary = "private-summary"
	v.Runs[0].Assessment.NextStep = "private-next"
	out, err := projectWebResolverHistory(v)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	for _, forbidden := range []string{"private-", "scope_id", "sensor_id", "reason", "profile", "endpoint", "challenge", "ticket"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	*v.Runs[0].Measurement.ResponseTimeNanoseconds = 1
	*v.Runs[0].Assessment.ExpectationMatched = false
	v.Runs[0].Measurement.Reply.RCode = 5
	if *out.Runs[0].Measurement.ResponseTimeNanoseconds != 0 || !*out.Runs[0].ExpectationMatched || *out.Runs[0].Measurement.RCode != 0 {
		t.Fatal("aliased native evidence")
	}
	for name, edit := range map[string]func(*api.ResolverHistory){
		"schema":           func(v *api.ResolverHistory) { v.SchemaVersion = 2 },
		"exact lookup":     func(v *api.ResolverHistory) { v.LookupRunID = v.Runs[0].RunID },
		"window":           func(v *api.ResolverHistory) { v.Since = &v.AsOf },
		"limit":            func(v *api.ResolverHistory) { v.Limit = 21 },
		"scan limit":       func(v *api.ResolverHistory) { v.ScanLimit = 257 },
		"nil runs":         func(v *api.ResolverHistory) { v.Runs = nil },
		"enrollment":       func(v *api.ResolverHistory) { v.Enrolled = false },
		"scope":            func(v *api.ResolverHistory) { v.Runs[0].Observer.ScopeID = "scope.other" },
		"duplicate":        func(v *api.ResolverHistory) { v.Runs = append(v.Runs, v.Runs[0]) },
		"interface":        func(v *api.ResolverHistory) { v.Runs[0].Observer.InterfaceName = "<img>" },
		"future":           func(v *api.ResolverHistory) { v.Runs[0].LastAuditAt = v.AsOf.Add(time.Second) },
		"old":              func(v *api.ResolverHistory) { v.Runs[0].LastAuditAt = v.Since.Add(-time.Second) },
		"missing terminal": func(v *api.ResolverHistory) { v.Runs[0].TerminalRetained = false },
		"unknown version":  func(v *api.ResolverHistory) { v.Runs[0].AuditSchemaVersion = 2 },
		"completed absent": func(v *api.ResolverHistory) { v.Runs[0].Measurement = nil },
		"blocked sample": func(v *api.ResolverHistory) {
			v.Runs[0].Outcome = "blocked"
			v.Runs[0].Reason = "preflight-unavailable"
		},
		"missing timing":   func(v *api.ResolverHistory) { v.Runs[0].Measurement.ResponseTimeNanoseconds = nil },
		"oversized timing": func(v *api.ResolverHistory) { *v.Runs[0].Measurement.ResponseTimeNanoseconds = int64(2 * time.Second) },
		"wrong result":     func(v *api.ResolverHistory) { v.Runs[0].Assessment.State = "timeout" },
		"confidence":       func(v *api.ResolverHistory) { v.Runs[0].Assessment.Confidence = "high" },
		"expectation":      func(v *api.ResolverHistory) { *v.Runs[0].Assessment.ExpectationMatched = false },
		"rcode":            func(v *api.ResolverHistory) { v.Runs[0].Measurement.Reply.RCode = 16 },
	} {
		t.Run(name, func(t *testing.T) {
			v := resolverHistoryWebFixture(t)
			edit(&v)
			if _, err := projectWebResolverHistory(v); err == nil {
				t.Fatal("accepted invalid history")
			}
		})
	}
}
func TestWebResolverProjectionPreservesNegativeAndMissingEvidence(t *testing.T) {
	for _, kind := range []string{"nxdomain", "unexpected", "timeout", "failed-response", "missing-terminal", "no-measurement", "unenrolled"} {
		t.Run(kind, func(t *testing.T) {
			v := resolverHistoryWebFixture(t)
			r := &v.Runs[0]
			switch kind {
			case "nxdomain", "unexpected":
				r.Measurement.Reply = &nq.DNSReply{RCode: 3}
				r.Assessment.State = "nxdomain"
				if kind == "nxdomain" {
					r.Selection.Expect = "nxdomain"
				} else {
					*r.Assessment.ExpectationMatched = false
				}
			case "timeout":
				r.Measurement.Exchange = nq.DNSTimeout
				r.Measurement.Reply = nil
				r.Measurement.ResponseTimeNanoseconds = nil
				r.Measurement.StartedAt = r.Measurement.CompletedAt.Add(-2 * time.Second)
				r.Assessment.State = "timeout"
				r.Assessment.ExpectationMatched = nil
			case "failed-response":
				r.Outcome = "failed"
				r.Reason = "execution-error"
			case "missing-terminal", "no-measurement":
				r.Measurement = nil
				r.Assessment = api.ResolverHistoricalAssessment{State: "unknown", Confidence: "unknown"}
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
				v.Runs = []api.ResolverHistoryRun{}
			}
			out, err := projectWebResolverHistory(v)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Runs) > 0 && (out.Runs[0].Evidence != r.Assessment.State || out.Runs[0].Outcome != r.Outcome) {
				t.Fatal("changed historical evidence")
			}
		})
	}
}
