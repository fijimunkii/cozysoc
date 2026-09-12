package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestQualityDiagnosisProcessUsesRetainedEvidenceWithoutExecution(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Unix controller process evidence")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skip("set process E2E binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "cz-diagnosis-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "state ?#%")
	s, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute).Round(0)
	end := now.Add(3 * time.Second)
	dend := end.Add(time.Second)
	zero := int64(0)
	scope := domain.NetworkScope{ID: "scope.fixture", Kind: "lan", EnrolledAt: now.Add(-time.Hour), Metadata: json.RawMessage(`{"device_watch":{"interface_name":"fixture0","interface_index":7,"prefixes":["192.168.50.0/24"]}}`)}
	if err := s.CreateNetworkScope(context.Background(), scope); err != nil {
		s.Close()
		t.Fatal(err)
	}
	g := gatewayrun.Event{SchemaVersion: 2, RunID: strings.Repeat("a", 32), ScopeID: scope.ID, Profile: gatewayrun.Profile, SelectionDigest: strings.Repeat("c", 64), InterfaceName: "fixture0", InterfaceIndex: 7, Source: "192.168.50.23", Target: "192.168.50.1", At: now}
	d := resolverrun.Event{SchemaVersion: 1, RunID: strings.Repeat("b", 32), Profile: resolverrun.Profile, At: end,
		Selection: nq.ResolverSelection{ID: "selection.test", ResolverID: "resolver.test", QueryID: "query.test", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryAAAA, Expect: nq.DNSExpectNXDOMAIN},
		Observer:  nq.Observer{ScopeID: scope.ID, SensorID: "resolver-audit", InterfaceName: "fixture0", InterfaceIndex: 7}}
	for _, phase := range []string{"authorized", "admitted", "finished"} {
		g.State, d.State = phase, phase
		if phase == "finished" {
			g.At = end
			g.Outcome = "completed"
			g.Measurement = &gatewayrun.Measurement{StartedAt: now, CompletedAt: &end, SendCalls: 3, AcceptedRequests: 3, Timeouts: 3, Complete: true}
			d.At = dend
			d.Outcome = "completed"
			d.Measurement = &resolverrun.Measurement{StartedAt: end, CompletedAt: dend, Exchange: nq.DNSResponseReceived, Request: nq.DNSRequestAccepted, Reply: &nq.DNSReply{RCode: 3}, ResponseTimeNanoseconds: &zero}
		}
		if err := s.InsertGatewayRunAudit(context.Background(), g); err != nil {
			s.Close()
			t.Fatal(err)
		}
		if err := s.InsertResolverRunAudit(context.Background(), d); err != nil {
			s.Close()
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	controller := startController(t, binary, dir)
	waitForReady(t, binary, dir, controller)
	assertGatewayExecutionDisabled(t, dir)
	assertResolverExecutionDisabled(t, dir)
	first := runCLIJSONArgs[api.QualityDiagnosis](t, binary, "network-quality-diagnosis", "--state-dir", dir)
	second := runCLIJSONArgs[api.QualityDiagnosis](t, binary, "network-quality-diagnosis", "--state-dir", dir)
	if first.Mode != "retained-comparison" || first.Conclusion != "icmp-misses-with-responses" || first.Confidence != "limited" || len(first.Compared) != 2 || first.AssessmentAt == nil || !first.AssessmentAt.Equal(dend) || first.EvidenceStart == nil || !first.EvidenceStart.Equal(now) || len(first.Selected) != 2 || first.Selected[1].Selection.Expect != "nxdomain" {
		t.Fatalf("changed historical diagnosis: %+v", first)
	}
	if !reflect.DeepEqual(first.Selected, second.Selected) || !first.AssessmentAt.Equal(*second.AssessmentAt) || first.Conclusion != second.Conclusion || !first.ReadAt.Before(second.ReadAt) {
		t.Fatal("read changed original evidence")
	}
	raw, _ := json.Marshal(first)
	for _, forbidden := range []string{readSecret(t, dir), "192.168.50", `"challenge"`, `"ticket"`, `"endpoint"`, `"name"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("diagnosis disclosed %s", forbidden)
		}
	}
	stopWithInterrupt(t, controller)
	s, err = storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.ReadQualityHistory(context.Background(), scope.ID, time.Now().UTC())
	if err != nil || len(page.Gateway.Runs) != 1 || len(page.Resolver.Runs) != 1 || !page.Gateway.Runs[0].TerminalRetained || !page.Resolver.Runs[0].TerminalRetained {
		t.Fatal("read changed retained runs", err)
	}
	if count, err := s.ObservationCount(context.Background()); err != nil || count != 0 {
		t.Fatal("diagnosis collected observations", err)
	}
}
