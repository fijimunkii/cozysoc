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
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type historyStoreStub struct {
	*fakeDeviceStore
	query storage.GatewayHistoryQuery
	page  storage.GatewayHistoryPage
	err   error
	calls int
	after func()
}

func (s *historyStoreStub) ReadGatewayHistory(_ context.Context, q storage.GatewayHistoryQuery) (storage.GatewayHistoryPage, error) {
	s.calls++
	s.query = q
	if s.after != nil {
		s.after()
	}
	return s.page, s.err
}

func TestHistoryUsesEnrollmentWithoutMonitoringOrOSReads(t *testing.T) {
	h, base, inspector := qualityFixture(t)
	store := &historyStoreStub{fakeDeviceStore: base}
	h.store = store
	for _, id := range []string{"", strings.Repeat("a", 32)} {
		result, err := h.GatewayHistory(context.Background(), api.GatewayHistoryParams{RunID: id})
		if err != nil || !result.Enrolled || result.Mode != "retained-history" || result.Runs == nil || store.query.ScopeID != "scope.home" || store.query.RunID != id || !store.query.AsOf.Equal(h.now()) {
			t.Fatalf("%+v %v", result, err)
		}
		if (result.Since == nil) != (id != "") || inspector.calls != 0 || h.gatewayRuns.control != nil || base.enrollMetadata != nil {
			t.Fatal("read queried OS, restored owner or created enrollment")
		}
	}
	base.activeScopes = nil
	result, err := h.GatewayHistory(context.Background(), api.GatewayHistoryParams{})
	if err != nil || result.Enrolled || len(result.Runs) != 0 || store.calls != 2 {
		t.Fatal("unconfigured read fabricated a scope", err)
	}
}

func TestHistoryEnrollmentChangeAndStoreFailuresDoNotPublish(t *testing.T) {
	for _, mode := range []string{"scope-changed", "store-error", "wrong-scope", "not-found", "bad-reference"} {
		t.Run(mode, func(t *testing.T) {
			h, base, inspector := qualityFixture(t)
			store := &historyStoreStub{fakeDeviceStore: base}
			h.store = store
			params := api.GatewayHistoryParams{}
			switch mode {
			case "scope-changed":
				store.after = func() { base.activeScopes[0].ID = "scope.new" }
			case "store-error":
				store.err = errors.New("private database diagnostic")
			case "wrong-scope":
				store.page.Runs = []gatewayrun.RetainedRun{{ScopeID: "scope.other"}}
			case "not-found":
				store.err = storage.ErrGatewayHistoryNotFound
			case "bad-reference":
				params.RunID = "invalid"
			}
			result, err := h.GatewayHistory(context.Background(), params)
			if err == nil || !reflect.DeepEqual(result, api.GatewayHistory{}) || inspector.calls != 0 {
				t.Fatalf("published out-of-context history: %+v %v", result, err)
			}
			if mode == "not-found" && !errors.Is(err, localapi.ErrReadTargetNotFound) {
				t.Fatal("lookup error not scoped")
			}
		})
	}
}

func TestHistoryProjectionPreservesOptionalFieldsWithoutAliases(t *testing.T) {
	h, base, _ := qualityFixture(t)
	end := h.now().Add(-time.Second)
	zero := int64(0)
	loss := 0.0
	source := gatewayrun.RetainedRun{RunID: strings.Repeat("a", 32), ScopeID: "scope.home", SchemaVersion: 2, Profile: gatewayrun.Profile, InterfaceName: "en0", InterfaceIndex: 7, Target: "192.168.50.1", Source: "192.168.50.23", LastAuditAt: end, TerminalRetained: true, Outcome: "completed",
		Measurement: &gatewayrun.Measurement{StartedAt: end.Add(-3 * time.Second), CompletedAt: &end, SendCalls: 3, AcceptedRequests: 3, Replies: 3, Complete: true, MeanRTTNanoseconds: &zero},
		Assessment:  gatewayrun.HistoricalAssessment{State: "all-replied", Confidence: "limited", ReplyLossPercent: &loss}}
	store := &historyStoreStub{fakeDeviceStore: base, page: storage.GatewayHistoryPage{Runs: []gatewayrun.RetainedRun{source}, ScanTruncated: true}}
	h.store = store
	got, err := h.GatewayHistory(context.Background(), api.GatewayHistoryParams{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"mean_rtt_ns":0`) || !got.ScanTruncated || got.Runs[0].GatewayRoleVerified {
		t.Fatal("lost representation boundaries")
	}
	*got.Runs[0].Measurement.MeanRTTNanoseconds = 42
	*got.Runs[0].Assessment.ReplyLossPercent = 100
	if zero != 0 || loss != 0 {
		t.Fatal("output aliases stored values")
	}
}

func TestHistoryCommandRejectsExecutionOptionsBeforeConnecting(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for _, args := range [][]string{{"--yes"}, {"192.168.50.1"}, {strings.Repeat("a", 32), strings.Repeat("b", 32)}, {"--scope-id", "scope.home"}} {
		if err := runGatewayHistoryCommand(context.Background(), args, out, out); err == nil {
			t.Fatal("invalid read command accepted")
		}
	}
}
