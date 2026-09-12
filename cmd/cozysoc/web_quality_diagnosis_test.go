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
)

func diagnosisWebFixture(t *testing.T) api.QualityDiagnosis {
	t.Helper()
	h, _ := diagnosisFixture(t)
	out, err := h.QualityDiagnosis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestWebQualityDiagnosisBoundary(t *testing.T) {
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
			h.loadQualityDiagnosis = func(ctx context.Context) (api.QualityDiagnosis, error) {
				calls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > webRequestTimeout {
					t.Error("unbounded read")
				}
				return diagnosisWebFixture(t), nil
			}
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000/api/network-quality/diagnosis"+tc.suffix, strings.NewReader(tc.body))
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

func TestWebQualityDiagnosisUnavailableNeverLeaksErrors(t *testing.T) {
	for _, mode := range []string{"missing", "error", "invalid", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
			if mode != "missing" {
				h.loadQualityDiagnosis = func(context.Context) (api.QualityDiagnosis, error) {
					if mode == "error" {
						return api.QualityDiagnosis{}, errors.New("private-path secret")
					}
					return api.QualityDiagnosis{}, nil
				}
			}
			r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/network-quality/diagnosis", nil)
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

func TestWebDiagnosisProjectionMinimizesAndOwnsInterpretation(t *testing.T) {
	v := diagnosisWebFixture(t)
	v.Summary = "private-summary"
	v.NextStep = "private-next"
	v.Limitations = []string{"private-limit"}
	out, err := projectWebQualityDiagnosis(v)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	for _, forbidden := range []string{"private-", "scope_id", "summary", "next_step", "limitations", "schema_version", "mode"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	original := *out.AssessmentAt
	*v.AssessmentAt = v.ReadAt
	*v.Selected[0].StartedAt = v.ReadAt
	v.Selected[1].Selection.Expect = "answer"
	if !out.AssessmentAt.Equal(original) || out.Selected[0].StartedAt.Equal(v.ReadAt) || out.Selected[1].Selection.Expect != "nxdomain" {
		t.Fatal("aliases native output")
	}
}
func TestWebDiagnosisRejectsContradictoryContext(t *testing.T) {
	for name, edit := range map[string]func(*api.QualityDiagnosis){
		"schema": func(v *api.QualityDiagnosis) { v.SchemaVersion = 3 }, "mode": func(v *api.QualityDiagnosis) { v.Mode = "current" },
		"window": func(v *api.QualityDiagnosis) { v.Since = v.ReadAt }, "limits": func(v *api.QualityDiagnosis) { v.RunLimitPerLayer = 21 }, "skew": func(v *api.QualityDiagnosis) { v.MaxCompletionSkewMS = 31000 },
		"arrays": func(v *api.QualityDiagnosis) { v.Compared = nil }, "enrollment": func(v *api.QualityDiagnosis) { v.Enrolled = false },
		"anchor": func(v *api.QualityDiagnosis) { v.AssessmentAt = &v.ReadAt }, "missing anchor": func(v *api.QualityDiagnosis) { v.AssessmentAt = nil },
		"interface": func(v *api.QualityDiagnosis) { v.Selected[0].InterfaceName = "<script>" }, "different interface": func(v *api.QualityDiagnosis) { v.Selected[0].InterfaceIndex++ },
		"IPv6": func(v *api.QualityDiagnosis) { v.Selected[1].Selection.Family = "ipv6" }, "duplicate layer": func(v *api.QualityDiagnosis) { v.Selected[1] = v.Selected[0] },
		"missing result": func(v *api.QualityDiagnosis) { v.Selected[0].SampleStatus = "missing-terminal" }, "missing sample": func(v *api.QualityDiagnosis) { v.Selected[0].CompletedAt = nil },
		"future": func(v *api.QualityDiagnosis) { v.Selected[0].LastAuditAt = v.ReadAt.Add(time.Second) }, "failed complete gateway": func(v *api.QualityDiagnosis) { v.Selected[0].ExecutionOutcome = "failed" },
		"wrong support": func(v *api.QualityDiagnosis) { v.Compared[0].RunID = strings.Repeat("c", 32) }, "duplicate support": func(v *api.QualityDiagnosis) { v.Compared[1] = v.Compared[0] },
		"wrong interval": func(v *api.QualityDiagnosis) { v.EvidenceStart = v.EvidenceEnd }, "confidence": func(v *api.QualityDiagnosis) { v.Confidence = "high" },
		"truncated match": func(v *api.QualityDiagnosis) { v.Truncated = true }, "external claim": func(v *api.QualityDiagnosis) { v.Conclusion = "external-check-issue-with-responses" },
	} {
		t.Run(name, func(t *testing.T) {
			v := diagnosisWebFixture(t)
			edit(&v)
			if _, err := projectWebQualityDiagnosis(v); err == nil {
				t.Fatal("accepted contradictory diagnosis")
			}
		})
	}
}
func TestWebDiagnosisAcceptsControllerUnknownStates(t *testing.T) {
	for _, mode := range []string{"unenrolled", "empty", "truncated", "missing-result", "mismatch", "stale", "failed-dns-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			h, s := diagnosisFixture(t)
			switch mode {
			case "unenrolled":
				s.activeScopes = nil
			case "empty":
				s.page.Gateway.Runs = nil
				s.page.Resolver.Runs = nil
			case "truncated":
				s.page.Resolver.ScanTruncated = true
			case "missing-result":
				s.page.Gateway.Runs[0].TerminalRetained = false
				s.page.Gateway.Runs[0].Outcome = "unknown"
				s.page.Gateway.Runs[0].Measurement = nil
			case "mismatch":
				s.page.Resolver.Runs[0].Selection.Family = "ipv6"
			case "stale":
				m := s.page.Gateway.Runs[0].Measurement
				m.StartedAt = m.StartedAt.Add(-time.Minute)
				end := m.CompletedAt.Add(-time.Minute)
				m.CompletedAt = &end
				s.page.Gateway.Runs[0].LastAuditAt = end
			case "failed-dns-cleanup":
				s.page.Resolver.Runs[0].Outcome = "failed"
				s.page.Resolver.Runs[0].Reason = "execution-error"
			}
			v, err := h.QualityDiagnosis(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			out, err := projectWebQualityDiagnosis(v)
			if err != nil || out.Conclusion != v.Conclusion {
				t.Fatalf("rejected controller diagnosis: %s %v", v.Conclusion, err)
			}
		})
	}
}
