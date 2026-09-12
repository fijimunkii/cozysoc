package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type httpsHistoryStoreStub struct {
	*fakeDeviceStore
	query storage.HTTPSHistoryQuery
	page  storage.HTTPSHistoryPage
	err   error
	calls int
	after func()
}

func (s *httpsHistoryStoreStub) ReadHTTPSHistory(_ context.Context, q storage.HTTPSHistoryQuery) (storage.HTTPSHistoryPage, error) {
	s.calls++
	s.query = q
	if s.after != nil {
		s.after()
	}
	return s.page, s.err
}

func TestHTTPSHistoryUsesEnrollmentWithoutMonitoringOrOSReads(t *testing.T) {
	h, base, inspector := qualityFixture(t)
	store := &httpsHistoryStoreStub{fakeDeviceStore: base}
	h.store = store
	for _, id := range []string{"", strings.Repeat("a", 32)} {
		result, err := h.HTTPSHistory(context.Background(), api.HTTPSHistoryParams{RunID: id})
		if err != nil || !result.Enrolled || result.Mode != "retained-history" || result.Runs == nil || store.query.ScopeID != "scope.home" || store.query.RunID != id || !store.query.AsOf.Equal(h.now()) {
			t.Fatalf("%+v %v", result, err)
		}
		if (result.Since == nil) != (id != "") || inspector.calls != 0 || h.httpsRuns.control != nil || base.enrollMetadata != nil {
			t.Fatal("read queried OS, restored owner or created enrollment")
		}
	}
	base.activeScopes = nil
	result, err := h.HTTPSHistory(context.Background(), api.HTTPSHistoryParams{})
	if err != nil || result.Enrolled || len(result.Runs) != 0 || store.calls != 2 {
		t.Fatal("unconfigured read fabricated a scope", err)
	}
}

func TestHTTPSHistoryEnrollmentChangeAndStoreFailuresDoNotPublish(t *testing.T) {
	for _, mode := range []string{"scope-changed", "store-error", "wrong-scope", "not-found", "bad-reference"} {
		t.Run(mode, func(t *testing.T) {
			h, base, inspector := qualityFixture(t)
			store := &httpsHistoryStoreStub{fakeDeviceStore: base}
			h.store = store
			params := api.HTTPSHistoryParams{}
			switch mode {
			case "scope-changed":
				store.after = func() { base.activeScopes[0].ID = "scope.new" }
			case "store-error":
				store.err = errors.New("private database diagnostic")
			case "wrong-scope":
				store.page.Runs = []httpsrun.RetainedRun{{Observer: nq.Observer{ScopeID: "scope.other"}}}
			case "not-found":
				store.err = storage.ErrHTTPSHistoryNotFound
			case "bad-reference":
				params.RunID = "invalid"
			}
			result, err := h.HTTPSHistory(context.Background(), params)
			if err == nil || !reflect.DeepEqual(result, api.HTTPSHistory{}) || inspector.calls != 0 {
				t.Fatalf("published out-of-context history: %+v %v", result, err)
			}
			if mode == "not-found" && !errors.Is(err, localapi.ErrReadTargetNotFound) {
				t.Fatal("lookup error not scoped")
			}
		})
	}
}

func TestHTTPSHistoryProjectionPreservesOptionalFieldsWithoutAliases(t *testing.T) {
	h, base, _ := qualityFixture(t)
	end := h.now().Add(-time.Second)
	zero := int64(0)
	matched := false
	source := httpsrun.RetainedRun{RunID: strings.Repeat("a", 32), Observer: nq.Observer{ScopeID: "scope.home", SensorID: "local", InterfaceName: "en0", InterfaceIndex: 7}, SchemaVersion: 1, Profile: httpsrun.Profile, LastAuditAt: end, TerminalRetained: true, Outcome: "completed",
		Measurement: &httpsrun.Measurement{StartedAt: end.Add(-time.Second), CompletedAt: end, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTimeNanoseconds: &zero},
		Assessment:  httpsrun.HistoricalAssessment{State: "status-response", Confidence: "limited", ExpectationMatched: &matched}}
	store := &httpsHistoryStoreStub{fakeDeviceStore: base, page: storage.HTTPSHistoryPage{Runs: []httpsrun.RetainedRun{source}, ScanTruncated: true}}
	h.store = store
	got, err := h.HTTPSHistory(context.Background(), api.HTTPSHistoryParams{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"response_time_ns":0`) || !got.ScanTruncated {
		t.Fatal("lost representation boundaries")
	}
	*got.Runs[0].Measurement.ResponseTimeNanoseconds = 42
	*got.Runs[0].Assessment.ExpectationMatched = true
	if zero != 0 || matched {
		t.Fatal("output aliases stored values")
	}
}

func TestHTTPSHistoryCommandRejectsExecutionOptionsBeforeConnecting(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for _, args := range [][]string{{"--yes"}, {"192.168.50.1"}, {strings.Repeat("a", 32), strings.Repeat("b", 32)}, {"--scope-id", "scope.home"}} {
		if err := runHTTPSHistoryCommand(context.Background(), args, out, out); err == nil {
			t.Fatal("invalid read command accepted")
		}
	}
}
