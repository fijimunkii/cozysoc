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

func qualityWebFixture(t *testing.T) api.LocalNetworkQuality {
	t.Helper()
	h, _, _ := qualityFixture(t)
	result, err := h.LocalNetworkQuality(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWebLocalQualityBoundary(t *testing.T) {
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
			h.loadLocalQuality = func(ctx context.Context) (api.LocalNetworkQuality, error) {
				calls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > webRequestTimeout {
					t.Error("unbounded read")
				}
				return qualityWebFixture(t), nil
			}
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000/api/network-quality"+tc.suffix, strings.NewReader(tc.body))
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

func TestWebLocalQualityProjectionMinimizesAndCopies(t *testing.T) {
	native := qualityWebFixture(t)
	native.Observer.ScopeID = "private-scope"
	native.Observer.SensorID = "private-sensor"
	native.Check.EvidenceID = "private-evidence"
	native.Check.Summary = "private-guidance"
	native.Check.NextStep = "private-path"
	native.Limitations = []string{"private-detail"}
	result, err := projectWebLocalQuality(native)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"private-", "scope_id", "sensor_id", "evidence_id", "summary", "next_step", "limitations", "prefixes", "latency", "loss", "session"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("native field leaked: %s", forbidden)
		}
	}
	*native.Check.AdministrativeUp = false
	if !*result.Check.AdministrativeUp {
		t.Fatal("projection aliases native pointer")
	}
	unenrolled, err := projectWebLocalQuality(api.LocalNetworkQuality{AsOf: result.AsOf})
	if err != nil || unenrolled.Check != nil || unenrolled.Observer != nil {
		t.Fatal("invented unenrolled evidence")
	}
}

func TestWebLocalQualityRejectsContradictions(t *testing.T) {
	for name, edit := range map[string]func(*api.LocalNetworkQuality){
		"missing timestamp":    func(v *api.LocalNetworkQuality) { v.AsOf = time.Time{} },
		"unenrolled evidence":  func(v *api.LocalNetworkQuality) { v.Enrolled = false },
		"missing observer":     func(v *api.LocalNetworkQuality) { v.Observer = nil },
		"missing check":        func(v *api.LocalNetworkQuality) { v.Check = nil },
		"hostile interface":    func(v *api.LocalNetworkQuality) { v.Observer.InterfaceName = "<script>" },
		"bad index":            func(v *api.LocalNetworkQuality) { v.Observer.InterfaceIndex = 0 },
		"wrong source":         func(v *api.LocalNetworkQuality) { v.Check.Source = "packet-capture" },
		"wrong layer":          func(v *api.LocalNetworkQuality) { v.Check.Layer = "internet" },
		"wrong method":         func(v *api.LocalNetworkQuality) { v.Check.Method = "icmp-echo" },
		"future start":         func(v *api.LocalNetworkQuality) { v.Check.StartedAt = v.AsOf.Add(time.Second) },
		"long read":            func(v *api.LocalNetworkQuality) { v.Check.StartedAt = v.AsOf.Add(-31 * time.Second) },
		"completion mismatch":  func(v *api.LocalNetworkQuality) { v.Check.CompletedAt = v.AsOf.Add(-time.Second) },
		"inflated freshness":   func(v *api.LocalNetworkQuality) { v.Check.FreshUntil = v.AsOf.Add(time.Hour) },
		"global verdict":       func(v *api.LocalNetworkQuality) { v.Check.State = "internet-up" },
		"missing metric":       func(v *api.LocalNetworkQuality) { v.Check.AdministrativeUp = nil },
		"contradictory metric": func(v *api.LocalNetworkQuality) { *v.Check.AdministrativeUp = false },
		"inflated confidence":  func(v *api.LocalNetworkQuality) { v.Check.Confidence = "high" },
		"gap on result":        func(v *api.LocalNetworkQuality) { v.Check.Gap = "network-changed" },
		"metric on gap": func(v *api.LocalNetworkQuality) {
			v.Check.State = "not-measured"
			v.Check.Confidence = "unknown"
			v.Check.Gap = "unsupported"
		},
		"invalid gap": func(v *api.LocalNetworkQuality) {
			v.Check.State = "not-measured"
			v.Check.Confidence = "unknown"
			v.Check.AdministrativeUp = nil
			v.Check.Gap = "isp-outage"
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := qualityWebFixture(t)
			edit(&value)
			if _, err := projectWebLocalQuality(value); err == nil {
				t.Fatal("invalid projection accepted")
			}
		})
	}
	for _, gap := range []string{"network-changed", "permission-required", "unsupported", "source-unavailable"} {
		value := qualityWebFixture(t)
		value.Check.State = "not-measured"
		value.Check.Confidence = "unknown"
		value.Check.Gap = gap
		value.Check.AdministrativeUp = nil
		if _, err := projectWebLocalQuality(value); err != nil {
			t.Fatalf("valid gap rejected: %v", err)
		}
	}
}

func TestWebLocalQualityUnavailableNeverLeaksErrors(t *testing.T) {
	for _, mode := range []string{"missing", "error", "invalid", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := newWebHandler("127.0.0.1:9000", t.TempDir(), "bootstrap", "session", nil)
			if mode != "missing" {
				h.loadLocalQuality = func(context.Context) (api.LocalNetworkQuality, error) {
					if mode == "error" {
						return api.LocalNetworkQuality{}, errors.New("private-path secret")
					}
					return api.LocalNetworkQuality{}, nil
				}
			}
			r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/network-quality", nil)
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
