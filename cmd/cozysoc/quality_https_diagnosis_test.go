package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func httpsDiagnosisFixture(t *testing.T) (*controllerAPIHandler, *diagnosisStoreStub) {
	t.Helper()
	h, s := diagnosisFixture(t)
	d := s.page.Resolver.Runs[0]
	end := d.LastAuditAt.Add(time.Second)
	zero := int64(0)
	e := httpsrun.Event{SchemaVersion: 1, RunID: strings.Repeat("c", 32), Profile: httpsrun.Profile, State: "finished", Outcome: "completed", At: end,
		Observer: d.Observer, Selection: nq.HTTPSSelection{ID: "selection.https", EndpointID: "endpoint.test", RequestID: "request.test", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 503},
		Measurement: &httpsrun.Measurement{StartedAt: d.LastAuditAt, CompletedAt: end, Exchange: nq.HTTPSResponseReceived, Request: nq.HTTPSRequestAccepted, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTimeNanoseconds: &zero}}
	r, err := httpsrun.DescribeRetainedRun([]httpsrun.Event{e}, h.now())
	if err != nil {
		t.Fatal(err)
	}
	s.page.HTTPS.Runs = []httpsrun.RetainedRun{r}
	return h, s
}

func refreshHTTPSDiagnosisFixture(t *testing.T, h *controllerAPIHandler, s *diagnosisStoreStub) {
	t.Helper()
	r := s.page.HTTPS.Runs[0]
	e := httpsrun.Event{SchemaVersion: r.SchemaVersion, RunID: r.RunID, Profile: r.Profile, State: "finished", Outcome: r.Outcome, Reason: r.Reason, At: r.LastAuditAt, Observer: r.Observer, Selection: r.Selection, Measurement: r.Measurement}
	if !r.TerminalRetained {
		e.State, e.Outcome, e.Reason, e.Measurement = "authorized", "", "", nil
	}
	updated, err := httpsrun.DescribeRetainedRun([]httpsrun.Event{e}, h.now())
	if err != nil {
		t.Fatal(err)
	}
	s.page.HTTPS.Runs[0] = updated
}

func TestHTTPSDiagnosisComparesAvailableLayersAndOriginalExpectations(t *testing.T) {
	for _, mode := range []string{"three", "dns-https-ipv6", "icmp-https", "https-only", "expected-error", "unexpected-error", "tls-failure", "failed-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			h, s := httpsDiagnosisFixture(t)
			want, count := "selected-checks-matched", 3
			r := &s.page.HTTPS.Runs[0]
			switch mode {
			case "dns-https-ipv6":
				s.page.Gateway.Runs = nil
				s.page.Resolver.Runs[0].Selection.Family = nq.FamilyIPv6
				r.Selection.Family = nq.FamilyIPv6
				count = 2
			case "icmp-https":
				s.page.Resolver.Runs = nil
				count = 2
			case "https-only":
				s.page.Gateway.Runs = nil
				s.page.Resolver.Runs = nil
				want = "insufficient-evidence"
				count = 0
			case "unexpected-error":
				r.Selection.ExpectedStatus = 204
				want = "external-check-issue-with-responses"
			case "tls-failure":
				r.Outcome = "failed"
				r.Reason = "execution-error"
				r.Measurement.Exchange = nq.HTTPSTLSError
				r.Measurement.Stage = nq.HTTPSTLS
				r.Measurement.Request = nq.HTTPSRequestNotSent
				r.Measurement.StatusCode = 0
				r.Measurement.ResponseTimeNanoseconds = nil
				want = "external-check-issue-with-responses"
			case "failed-cleanup":
				r.Outcome = "failed"
				r.Reason = "execution-error"
			}
			refreshHTTPSDiagnosisFixture(t, h, s)
			first, err := h.QualityDiagnosis(context.Background())
			if err != nil || first.Conclusion != want || len(first.Compared) != count || first.SchemaVersion != 2 {
				t.Fatalf("%+v %v", first, err)
			}
			projected, err := projectWebQualityDiagnosis(first)
			if err != nil || len(projected.Compared) != count {
				t.Fatalf("web projection: %+v %v", projected, err)
			}
			raw, _ := json.Marshal(projected)
			for _, forbidden := range []string{"scope_id", "sensor_id", "profile\"", "summary", "reason", "192.168.50"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatalf("leaked %s", forbidden)
				}
			}
			original := h.now()
			h.now = func() time.Time { return original.Add(time.Hour) }
			second, err := h.QualityDiagnosis(context.Background())
			if err != nil || !reflect.DeepEqual(first.Selected, second.Selected) || first.Conclusion != second.Conclusion || !first.AssessmentAt.Equal(*second.AssessmentAt) {
				t.Fatal("read changed historical evidence", err)
			}
			if h.networkInspector.(*qualityInspector).calls != 0 || h.httpsRuns.control != nil {
				t.Fatal("diagnosis reached collection")
			}
		})
	}
}

func TestHTTPSDiagnosisDoesNotCherryPickAroundLatestEvidence(t *testing.T) {
	for _, mode := range []string{"missing-terminal", "incomplete", "late-incomplete", "stale", "different-interface", "different-family", "truncated", "scan-truncated"} {
		t.Run(mode, func(t *testing.T) {
			h, s := httpsDiagnosisFixture(t)
			old := s.page.HTTPS.Runs[0]
			r := &s.page.HTTPS.Runs[0]
			want := "latest-run-unmeasured"
			switch mode {
			case "missing-terminal":
				r.TerminalRetained = false
				r.Outcome = "unknown"
				r.Measurement = nil
			case "incomplete", "late-incomplete":
				r.Outcome = "failed"
				r.Reason = "execution-error"
				if mode == "late-incomplete" {
					r.Measurement.StartedAt = r.Measurement.CompletedAt.Add(-30 * time.Second)
				}
				r.Measurement.Exchange = nq.HTTPSIncomplete
				r.Measurement.StatusCode = 0
				r.Measurement.ResponseTimeNanoseconds = nil
			case "stale":
				r.LastAuditAt = r.LastAuditAt.Add(-time.Minute)
				r.Measurement.StartedAt = r.Measurement.StartedAt.Add(-time.Minute)
				r.Measurement.CompletedAt = r.Measurement.CompletedAt.Add(-time.Minute)
				want = "insufficient-evidence"
			case "different-interface":
				r.Observer.InterfaceIndex++
				want = "observation-context-mismatch"
			case "different-family":
				r.Selection.Family = nq.FamilyIPv6
				want = "observation-context-mismatch"
			case "truncated":
				s.page.HTTPS.Truncated = true
				want = "history-incomplete"
			case "scan-truncated":
				s.page.HTTPS.ScanTruncated = true
				want = "history-incomplete"
			}
			refreshHTTPSDiagnosisFixture(t, h, s)
			if mode == "missing-terminal" {
				old.RunID = strings.Repeat("d", 32)
				old.LastAuditAt = old.LastAuditAt.Add(-time.Second)
				s.page.HTTPS.Runs = append(s.page.HTTPS.Runs, old)
			}
			out, err := h.QualityDiagnosis(context.Background())
			if err != nil || out.Conclusion != want || len(out.Compared) != 0 || out.Confidence != "unknown" {
				t.Fatalf("%+v %v", out, err)
			}
			if _, err := projectWebQualityDiagnosis(out); err != nil {
				t.Fatal("web rejected unknown", err)
			}
		})
	}
}

func TestWebHTTPSDiagnosisRejectsTamperingAndOwnsEvidence(t *testing.T) {
	for _, mode := range []string{"status", "time", "interface", "run", "expectation", "missing-https", "extra-https", "subset", "schema", "matched-contradiction", "external-contradiction"} {
		t.Run(mode, func(t *testing.T) {
			h, _ := httpsDiagnosisFixture(t)
			v, err := h.QualityDiagnosis(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			r := &v.Selected[2]
			switch mode {
			case "status":
				r.HTTPS.Measurement.StatusCode = 600
			case "time":
				r.HTTPS.Measurement.CompletedAt = r.HTTPS.Measurement.CompletedAt.Add(-time.Second)
			case "interface":
				r.HTTPS.Observer.InterfaceIndex++
			case "run":
				r.HTTPS.RunID = strings.Repeat("d", 32)
			case "expectation":
				r.HTTPS.Selection.ExpectedStatus = 204
			case "missing-https":
				r.HTTPS = nil
			case "extra-https":
				v.Selected[0].HTTPS = r.HTTPS
			case "subset":
				v.Compared = v.Compared[:2]
			case "matched-contradiction":
				r.HTTPS.Selection.ExpectedStatus = 204
				matched := false
				r.HTTPS.Assessment.ExpectationMatched = &matched
			case "external-contradiction":
				v.Conclusion = "external-check-issue-with-responses"
			case "schema":
				v.SchemaVersion = 1
			}
			if _, err := projectWebQualityDiagnosis(v); err == nil {
				t.Fatal("accepted inconsistent HTTPS diagnosis")
			}
		})
	}
	h, s := httpsDiagnosisFixture(t)
	v, err := h.QualityDiagnosis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	web, err := projectWebQualityDiagnosis(v)
	if err != nil {
		t.Fatal(err)
	}
	*v.Selected[2].HTTPS.Measurement.ResponseTimeNanoseconds = 42
	v.Selected[2].HTTPS.Selection.ExpectedStatus = 204
	if *web.Selected[2].HTTPS.Measurement.ResponseTimeNanoseconds != 0 || web.Selected[2].HTTPS.Selection.ExpectedStatus != 503 || s.page.HTTPS.Runs[0].Selection.ExpectedStatus != 503 {
		t.Fatal("aliased evidence")
	}
}
