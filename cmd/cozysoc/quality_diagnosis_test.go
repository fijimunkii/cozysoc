package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type diagnosisStoreStub struct {
	*fakeDeviceStore
	page  storage.QualityHistoryWithHTTPSPage
	scope string
	at    time.Time
	calls int
	err   error
	after func()
}

func (s *diagnosisStoreStub) ReadQualityHistoryWithHTTPS(_ context.Context, scope string, at time.Time) (storage.QualityHistoryWithHTTPSPage, error) {
	s.calls++
	s.scope, s.at = scope, at
	if s.after != nil {
		s.after()
	}
	return s.page, s.err
}
func diagnosisFixture(t *testing.T) (*controllerAPIHandler, *diagnosisStoreStub) {
	t.Helper()
	h, base, _ := qualityFixture(t)
	end := h.now().Add(-time.Minute)
	start := end.Add(-3 * time.Second)
	zero := int64(0)
	g := gatewayrun.RetainedRun{RunID: strings.Repeat("a", 32), ScopeID: "scope.home", Profile: gatewayrun.Profile, SchemaVersion: 2, InterfaceName: "en0", InterfaceIndex: 7, Target: "192.168.50.1", Source: "192.168.50.23", LastAuditAt: end, AuthorizationRetained: true, AdmissionRetained: true, TerminalRetained: true, Outcome: "completed",
		Measurement: &gatewayrun.Measurement{StartedAt: start, CompletedAt: &end, SendCalls: 3, AcceptedRequests: 3, Replies: 3, Complete: true, MeanRTTNanoseconds: &zero}}
	dend := end.Add(time.Second)
	d := resolverrun.RetainedRun{RunID: strings.Repeat("b", 32), Profile: resolverrun.Profile, SchemaVersion: 1, Observer: nq.Observer{ScopeID: "scope.home", SensorID: "resolver-audit", InterfaceName: "en0", InterfaceIndex: 7}, LastAuditAt: dend, AuthorizationRetained: true, AdmissionRetained: true, TerminalRetained: true, Outcome: "completed",
		Selection:   nq.ResolverSelection{ID: "selection.test", ResolverID: "resolver.test", QueryID: "query.test", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryAAAA, Expect: nq.DNSExpectNXDOMAIN},
		Measurement: &resolverrun.Measurement{StartedAt: end, CompletedAt: dend, Exchange: nq.DNSResponseReceived, Request: nq.DNSRequestAccepted, Reply: &nq.DNSReply{RCode: 3}, ResponseTimeNanoseconds: &zero}}
	store := &diagnosisStoreStub{fakeDeviceStore: base, page: storage.QualityHistoryWithHTTPSPage{QualityHistoryPage: storage.QualityHistoryPage{Gateway: storage.GatewayHistoryPage{Runs: []gatewayrun.RetainedRun{g}}, Resolver: storage.ResolverHistoryPage{Runs: []resolverrun.RetainedRun{d}}}}}
	h.store = store
	return h, store
}
func TestDiagnosisReadUsesHistoricalAnchorWithoutOSOrConsent(t *testing.T) {
	h, s := diagnosisFixture(t)
	original := h.now()
	first, err := h.QualityDiagnosis(context.Background())
	if err != nil || first.Conclusion != "selected-checks-matched" || first.Mode != "retained-comparison" || first.Confidence != "limited" || len(first.Compared) != 2 || s.scope != "scope.home" || !s.at.Equal(original) {
		t.Fatalf("%+v %v", first, err)
	}
	if h.resolverRuns.control != nil || h.networkInspector.(*qualityInspector).calls != 0 {
		t.Fatal("read reached collection or consent")
	}
	h.now = func() time.Time { return original.Add(time.Hour) }
	second, err := h.QualityDiagnosis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.AssessmentAt.Equal(*second.AssessmentAt) || !first.EvidenceStart.Equal(*second.EvidenceStart) || !first.EvidenceEnd.Equal(*second.EvidenceEnd) || first.Conclusion != second.Conclusion || first.ReadAt.Equal(second.ReadAt) {
		t.Fatal("read freshened historical sample")
	}
	*second.Selected[0].CompletedAt = original
	second.Selected[1].Selection.Expect = "answer"
	if !s.page.Gateway.Runs[0].Measurement.CompletedAt.Equal(*first.Selected[0].CompletedAt) || s.page.Resolver.Runs[0].Selection.Expect != nq.DNSExpectNXDOMAIN {
		t.Fatal("aliased source")
	}
}
func TestDiagnosisLatestUnknownAndIncompleteDoNotReviveOlderSuccess(t *testing.T) {
	for _, kind := range []string{"gateway", "resolver"} {
		t.Run(kind, func(t *testing.T) {
			h, s := diagnosisFixture(t)
			if kind == "gateway" {
				r := s.page.Gateway.Runs[0]
				r.RunID = strings.Repeat("c", 32)
				r.LastAuditAt = h.now().Add(-time.Second)
				r.TerminalRetained = false
				r.Outcome = "unknown"
				r.Measurement = nil
				s.page.Gateway.Runs = append(s.page.Gateway.Runs, r)
			} else {
				r := s.page.Resolver.Runs[0]
				r.RunID = strings.Repeat("c", 32)
				r.LastAuditAt = h.now().Add(-time.Second)
				r.Outcome = "failed"
				r.Reason = "execution-error"
				r.Measurement = nil
				s.page.Resolver.Runs = append(s.page.Resolver.Runs, r)
			}
			out, err := h.QualityDiagnosis(context.Background())
			if err != nil || out.Conclusion != "latest-run-unmeasured" || len(out.Compared) != 0 {
				t.Fatalf("reused old success: %+v %v", out, err)
			}
		})
	}
}
func TestDiagnosisHonestGapsAndContextBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*controllerAPIHandler, *diagnosisStoreStub)
	}{
		{"unenrolled", "not-enrolled", func(h *controllerAPIHandler, s *diagnosisStoreStub) { s.activeScopes = nil }},
		{"empty", "insufficient-evidence", func(h *controllerAPIHandler, s *diagnosisStoreStub) { s.page = storage.QualityHistoryWithHTTPSPage{} }},
		{"truncated", "history-incomplete", func(h *controllerAPIHandler, s *diagnosisStoreStub) { s.page.Gateway.Truncated = true }},
		{"scan limited", "history-incomplete", func(h *controllerAPIHandler, s *diagnosisStoreStub) { s.page.Resolver.ScanTruncated = true }},
		{"different family", "observation-context-mismatch", func(h *controllerAPIHandler, s *diagnosisStoreStub) {
			s.page.Resolver.Runs[0].Selection.Family = nq.FamilyIPv6
		}},
		{"different interface", "observation-context-mismatch", func(h *controllerAPIHandler, s *diagnosisStoreStub) {
			s.page.Resolver.Runs[0].Observer.InterfaceName = "en1"
		}},
		{"unexpected NXDOMAIN", "dns-query-issue-with-responses", func(h *controllerAPIHandler, s *diagnosisStoreStub) {
			s.page.Resolver.Runs[0].Selection.Expect = nq.DNSExpectAnswer
		}},
		{"failed cleanup retained reply", "selected-checks-matched", func(h *controllerAPIHandler, s *diagnosisStoreStub) {
			s.page.Resolver.Runs[0].Outcome = "failed"
			s.page.Resolver.Runs[0].Reason = "execution-error"
		}},
		{"old sample", "insufficient-evidence", func(h *controllerAPIHandler, s *diagnosisStoreStub) {
			m := s.page.Gateway.Runs[0].Measurement
			m.StartedAt = m.StartedAt.Add(-time.Minute)
			end := m.CompletedAt.Add(-time.Minute)
			m.CompletedAt = &end
			s.page.Gateway.Runs[0].LastAuditAt = end
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, s := diagnosisFixture(t)
			tc.edit(h, s)
			out, err := h.QualityDiagnosis(context.Background())
			if err != nil || out.Conclusion != tc.want {
				t.Fatalf("%+v %v", out, err)
			}
			if tc.name == "unenrolled" && s.calls != 0 {
				t.Fatal("unconfigured read consulted histories")
			}
		})
	}
}
func TestDiagnosisRejectsChangedEnrollmentInvalidOrAmbiguousHistory(t *testing.T) {
	for _, mode := range []string{"store-error", "scope-changed", "wrong-scope", "duplicate", "latest-tie", "future", "invalid-measurement", "missing-end"} {
		t.Run(mode, func(t *testing.T) {
			h, s := diagnosisFixture(t)
			switch mode {
			case "store-error":
				s.err = errors.New("private-diagnostic")
			case "scope-changed":
				s.after = func() { s.activeScopes[0].ID = "scope.new" }
			case "wrong-scope":
				s.page.Resolver.Runs[0].Observer.ScopeID = "scope.other"
			case "duplicate":
				s.page.Gateway.Runs = append(s.page.Gateway.Runs, s.page.Gateway.Runs[0])
			case "latest-tie":
				r := s.page.Gateway.Runs[0]
				r.RunID = strings.Repeat("c", 32)
				s.page.Gateway.Runs = append(s.page.Gateway.Runs, r)
			case "future":
				s.page.Gateway.Runs[0].LastAuditAt = h.now().Add(time.Second)
			case "missing-end":
				s.page.Gateway.Runs[0].Measurement.CompletedAt = nil
			case "invalid-measurement":
				s.page.Gateway.Runs[0].Measurement.Replies = 4
			}
			out, err := h.QualityDiagnosis(context.Background())
			if err == nil || !reflect.DeepEqual(out, api.QualityDiagnosis{}) {
				t.Fatalf("published invalid history: %+v %v", out, err)
			}
		})
	}
}

func TestDiagnosisCancellationDoesNotPublishFinishedRead(t *testing.T) {
	h, s := diagnosisFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.after = cancel
	out, err := h.QualityDiagnosis(ctx)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, api.QualityDiagnosis{}) {
		t.Fatalf("published canceled read: %+v %v", out, err)
	}
}

func TestDiagnosisLatestSelectionIsIndependentOfHistoryOrder(t *testing.T) {
	h, s := diagnosisFixture(t)
	newest := s.page.Gateway.Runs[0]
	a, b := newest, newest
	a.RunID, b.RunID = strings.Repeat("c", 32), strings.Repeat("d", 32)
	a.LastAuditAt, b.LastAuditAt = newest.LastAuditAt.Add(-time.Minute), newest.LastAuditAt.Add(-time.Minute)
	s.page.Gateway.Runs = []gatewayrun.RetainedRun{a, b, newest}
	first, err := h.QualityDiagnosis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.page.Gateway.Runs = []gatewayrun.RetainedRun{newest, b, a}
	second, err := h.QualityDiagnosis(context.Background())
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("order selected a different latest run", err)
	}
}
