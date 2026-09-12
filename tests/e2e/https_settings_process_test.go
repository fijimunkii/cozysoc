package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestHTTPSSettingsProcessPersistsWithoutExecution(t *testing.T) {
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
	dir := filepath.Join(t.TempDir(), "state")
	s, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.NetworkScope{ID: "scope.fixture", Kind: "lan", EnrolledAt: time.Now().Add(-time.Hour), Metadata: json.RawMessage(`{"device_watch":{"interface_name":"fixture0","interface_index":7,"prefixes":["192.0.2.0/24"]}}`)}
	if err = s.CreateNetworkScope(context.Background(), scope); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	controller := startController(t, binary, dir)
	waitForReady(t, binary, dir, controller)
	saved := runCLIJSONArgs[api.HTTPSSettingsResult](t, binary, "https-save", "--state-dir", dir, "--endpoint", "198.51.100.20:443", "--server-name", "Private.Example", "--request-target", "/check?test=1", "--family", "ipv4", "--method", "HEAD", "--expected-status", "204", "--destination-policy", "exact-endpoint")
	if saved.SchemaVersion != 1 || saved.Mode != "configuration-only" || saved.ConsentGranted || len(saved.Items) != 1 || saved.Items[0].Settings.ServerName != "private.example" || saved.Items[0].ScopeID != scope.ID {
		t.Fatal("invalid settings disclosure")
	}
	id := saved.Items[0].SelectionID
	if !localapi.ValidHTTPSSelectionID(id) {
		t.Fatal("invalid reference")
	}
	for _, method := range []string{"network-quality.https-run", "network-quality.https-check", "network-quality.https-approve", "network-quality.https-plan"} {
		if _, err := localapi.NewClient(dir).Call(context.Background(), method); err == nil || !strings.Contains(err.Error(), "method_not_found") {
			t.Fatal("settings exposed execution/route authority")
		}
	}
	assertGatewayExecutionDisabled(t, dir)
	assertResolverExecutionDisabled(t, dir)
	coverage := runCLIJSONArgs[map[string]any](t, binary, "device-watch-coverage", "--state-dir", dir)
	if coverage["configured"] != false {
		t.Fatal("settings enabled Device Watch")
	}
	stopWithInterrupt(t, controller)
	if strings.Contains(controller.logs(), "private.example") || strings.Contains(controller.logs(), "198.51.100.20") || strings.Contains(controller.logs(), "/check?test=1") {
		t.Fatal("controller logs leaked settings")
	}
	controller = startController(t, binary, dir)
	waitForReady(t, binary, dir, controller)
	listed := runCLIJSONArgs[api.HTTPSSettingsResult](t, binary, "https-list", "--state-dir", dir)
	if len(listed.Items) != 1 || listed.Items[0] != saved.Items[0] || listed.ConsentGranted {
		t.Fatal("restart changed settings")
	}
	retired := runCLIJSONArgs[api.HTTPSRetireResult](t, binary, "https-retire", "--state-dir", dir, id)
	if retired.State != "retired" || retired.SelectionID != id {
		t.Fatal("retirement failed")
	}
	listed = runCLIJSONArgs[api.HTTPSSettingsResult](t, binary, "https-list", "--state-dir", dir)
	if len(listed.Items) != 0 {
		t.Fatal("retired settings active")
	}
	assertCLIErrorContains(t, binary, "unavailable", "https-retire", "--state-dir", dir, id)
	stopWithInterrupt(t, controller)
	s, err = storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.ActiveHTTPSConfiguration(context.Background(), id); err != storage.ErrHTTPSConfiguration {
		t.Fatal("retired selection resolved")
	}
	if n, err := s.ObservationCount(context.Background()); err != nil || n != 0 {
		t.Fatal("settings created observations")
	}
	page, err := s.ReadQualityHistory(context.Background(), scope.ID, time.Now())
	if err != nil || len(page.Gateway.Runs) != 0 || len(page.Resolver.Runs) != 0 {
		t.Fatal("settings created probe audits")
	}
}
