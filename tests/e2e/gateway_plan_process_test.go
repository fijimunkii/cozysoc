package e2e

import (
	"context"
	"database/sql"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestControllerProcessGatewayPreviewNeverAuthorizesOrProbes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process boundary test, not hardware certification")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc binary", e2eBinaryEnv)
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	process := startController(t, binary, stateDir)
	waitForReady(t, binary, stateDir, process)
	assertCLIErrorContains(t, binary, "not_found", "network-quality-plan", "--state-dir", stateDir, "192.168.50.1")
	assertCLIErrorContains(t, binary, "requires TARGET_IPV4", "network-quality-plan", "--state-dir", stateDir)
	assertCLIErrorContains(t, binary, "private IPv4", "network-quality-plan", "--state-dir", stateDir, "gateway.local")
	assertCLIErrorContains(t, binary, "flag provided but not defined", "network-quality-plan", "--state-dir", stateDir, "--execute", "192.168.50.1")

	list := runCLIJSONArgs[processNetworkList](t, binary, "networks", "--state-dir", stateDir)
	var candidate processNetworkInterface
	var target string
	for _, item := range list.Candidates {
		for _, raw := range item.Prefixes {
			prefix, err := netip.ParsePrefix(raw)
			if err == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && prefix.Bits() <= 30 {
				candidate, target = item, prefix.Masked().Addr().Next().String()
				break
			}
		}
		if target != "" {
			break
		}
	}
	if target == "" {
		t.Skip("no RFC1918 interface metadata for the enrolled-preview portion")
	}
	// This arbitrary prefix member is deliberately NOT claimed to be a gateway.
	// Neither enumeration, enrollment nor preview sends traffic to it.
	enrolled := runCLIJSONArgs[processNetworkEnrollResult](t, binary, "network-enroll", "--state-dir", stateDir, candidate.InterfaceName)
	for read := 0; read < 2; read++ {
		plan := runCLIJSONArgs[api.GatewayCheckPlan](t, binary, "network-quality-plan", "--state-dir", stateDir, target)
		if plan.Mode != "preview-only" || plan.ExecutionAvailable || plan.ConsentGranted || plan.Target.RoleVerified ||
			plan.Target.Address != target || plan.Target.Family != "ipv4" || plan.Binding.ScopeID != enrolled.ScopeID ||
			plan.Binding.InterfaceName != candidate.InterfaceName || plan.Binding.InterfaceIndex != candidate.InterfaceIndex ||
			plan.Method != "icmp-echo" || plan.ProposedBudget.MaxAttempts != 3 || plan.ProposedBudget.MaxICMPRequestBytes != 120 ||
			!plan.ReviewExpiresAt.Equal(plan.CreatedAt.Add(30*time.Second)) || len(plan.Limitations) != 7 {
			t.Fatalf("unexpected preview: %+v", plan)
		}
	}
	client := localapi.NewClient(stateDir)
	_, err = client.CallWithParams(context.Background(), api.MethodNetworkQualityGatewayPlan, map[string]any{"target": target, "consent": true})
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("preview accepted consent: %v", err)
	}
	_, err = client.Call(context.Background(), "network-quality.gateway-run")
	if err == nil || !strings.Contains(err.Error(), "method_not_found") {
		t.Fatalf("executor unexpectedly available: %v", err)
	}
	coverage := runCLIJSONArgs[map[string]any](t, binary, "device-watch-coverage", "--state-dir", stateDir)
	if coverage["configured"] != false {
		t.Fatal("review enabled Device Watch")
	}
	stopWithInterrupt(t, process)
	assertNetworkEnrollmentAudit(t, filepath.Join(stateDir, storage.Filename), enrolled.ScopeID, 1)
	db, err := sql.Open("sqlite", filepath.Join(stateDir, storage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var observations, coverageSamples, audits int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM observations), (SELECT count(*) FROM coverage_samples), (SELECT count(*) FROM audit_events)`).Scan(&observations, &coverageSamples, &audits); err != nil {
		t.Fatal(err)
	}
	if observations != 0 || coverageSamples != 0 || audits != 1 {
		t.Fatalf("preview wrote state: observations=%d coverage=%d audits=%d", observations, coverageSamples, audits)
	}
}
