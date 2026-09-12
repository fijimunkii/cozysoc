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
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type dnsHistoryStoreStub struct {
	*fakeDeviceStore
	query storage.ResolverHistoryQuery
	page  storage.ResolverHistoryPage
	err   error
	calls int
	after func()
}

func (s *dnsHistoryStoreStub) ReadResolverHistory(_ context.Context, q storage.ResolverHistoryQuery) (storage.ResolverHistoryPage, error) {
	s.calls++
	s.query = q
	if s.after != nil {
		s.after()
	}
	return s.page, s.err
}

func TestDNSHistoryUsesEnrollmentWithoutMonitoringOrOSReads(t *testing.T) {
	h, base, inspector := qualityFixture(t)
	store := &dnsHistoryStoreStub{fakeDeviceStore: base}
	h.store = store
	for _, id := range []string{"", strings.Repeat("a", 32)} {
		result, err := h.ResolverHistory(context.Background(), api.ResolverHistoryParams{RunID: id})
		if err != nil || !result.Enrolled || result.Mode != "retained-history" || result.Runs == nil || store.query.ScopeID != "scope.home" || store.query.RunID != id || !store.query.AsOf.Equal(h.now()) {
			t.Fatalf("%+v %v", result, err)
		}
		if (result.Since == nil) != (id != "") || inspector.calls != 0 || h.resolverRuns.control != nil || base.enrollMetadata != nil {
			t.Fatal("read queried OS, restored owner or created enrollment")
		}
	}
	base.activeScopes = nil
	result, err := h.ResolverHistory(context.Background(), api.ResolverHistoryParams{})
	if err != nil || result.Enrolled || len(result.Runs) != 0 || store.calls != 2 {
		t.Fatal("unconfigured read fabricated a scope", err)
	}
}

func TestDNSHistoryEnrollmentChangeAndStoreFailuresDoNotPublish(t *testing.T) {
	for _, mode := range []string{"scope-changed", "store-error", "wrong-scope", "not-found", "bad-reference"} {
		t.Run(mode, func(t *testing.T) {
			h, base, inspector := qualityFixture(t)
			store := &dnsHistoryStoreStub{fakeDeviceStore: base}
			h.store = store
			params := api.ResolverHistoryParams{}
			switch mode {
			case "scope-changed":
				store.after = func() { base.activeScopes[0].ID = "scope.new" }
			case "store-error":
				store.err = errors.New("private database diagnostic")
			case "wrong-scope":
				store.page.Runs = []resolverrun.RetainedRun{{Observer: nq.Observer{ScopeID: "scope.other"}}}
			case "not-found":
				store.err = storage.ErrResolverHistoryNotFound
			case "bad-reference":
				params.RunID = "invalid"
			}
			result, err := h.ResolverHistory(context.Background(), params)
			if err == nil || !reflect.DeepEqual(result, api.ResolverHistory{}) || inspector.calls != 0 {
				t.Fatalf("published out-of-context history: %+v %v", result, err)
			}
			if mode == "not-found" && !errors.Is(err, localapi.ErrReadTargetNotFound) {
				t.Fatal("lookup error not scoped")
			}
		})
	}
}

func TestDNSHistoryProjectionPreservesOptionalFieldsWithoutAliases(t *testing.T) {
	h, base, _ := qualityFixture(t)
	end := h.now().Add(-time.Second)
	zero := int64(0)
	matched := false
	source := resolverrun.RetainedRun{RunID: strings.Repeat("a", 32), Observer: nq.Observer{ScopeID: "scope.home", SensorID: "local", InterfaceName: "en0", InterfaceIndex: 7}, SchemaVersion: 1, Profile: resolverrun.Profile, LastAuditAt: end, TerminalRetained: true, Outcome: "completed",
		Measurement: &resolverrun.Measurement{StartedAt: end.Add(-time.Second), CompletedAt: end, Request: nq.DNSRequestAccepted, Exchange: nq.DNSResponseReceived, Reply: &nq.DNSReply{RCode: 3}, ResponseTimeNanoseconds: &zero},
		Assessment:  resolverrun.HistoricalAssessment{State: "nxdomain", Confidence: "limited", ExpectationMatched: &matched}}
	store := &dnsHistoryStoreStub{fakeDeviceStore: base, page: storage.ResolverHistoryPage{Runs: []resolverrun.RetainedRun{source}, ScanTruncated: true}}
	h.store = store
	got, err := h.ResolverHistory(context.Background(), api.ResolverHistoryParams{})
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

func TestDNSHistoryCommandRejectsExecutionOptionsBeforeConnecting(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for _, args := range [][]string{{"--yes"}, {"192.168.50.1"}, {strings.Repeat("a", 32), strings.Repeat("b", 32)}, {"--scope-id", "scope.home"}} {
		if err := runResolverHistoryCommand(context.Background(), args, out, out); err == nil {
			t.Fatal("invalid read command accepted")
		}
	}
}
