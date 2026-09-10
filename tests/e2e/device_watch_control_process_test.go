package e2e

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/storage"
	_ "modernc.org/sqlite"
)

type processDeviceWatchControlResult struct {
	ScopeID string `json:"scope_id,omitempty"`
	Changed bool   `json:"changed"`
	Active  bool   `json:"active"`
	State   struct {
		Desired      string `json:"desired"`
		Verification string `json:"verification"`
	} `json:"state"`
}

type processCapabilityStateList struct {
	Capabilities []struct {
		Configured bool `json:"configured"`
		Manifest   struct {
			ID string `json:"id"`
		} `json:"manifest"`
		State struct {
			Desired      string `json:"desired"`
			Verification string `json:"verification"`
		} `json:"state"`
	} `json:"capabilities"`
}

func TestControllerProcessDeviceWatchControlFailsClosedOnUnsupportedLinuxRuntime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process Device Watch control E2E currently targets the Linux CI reference runner")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc binary to run process E2E", e2eBinaryEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	controller := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, controller)
	networks := runCLIJSONArgs[processNetworkList](t, absoluteBinary, "networks", "--state-dir", stateDir)
	if len(networks.Candidates) == 0 {
		t.Fatal("Linux CI runner exposed no eligible local interface candidates")
	}
	enrolled := runCLIJSONArgs[processNetworkEnrollResult](t, absoluteBinary,
		"network-enroll", "--state-dir", stateDir, networks.Candidates[0].InterfaceName)
	if !enrolled.Changed || enrolled.ScopeID == "" {
		t.Fatalf("failed to enroll Device Watch authorization scope: %+v", enrolled)
	}

	assertCLIErrorContains(t, absoluteBinary, "precondition_failed",
		"device-watch-enable", "--state-dir", stateDir)
	assertProcessDeviceWatchDisabled(t, absoluteBinary, stateDir, false)

	disabled := runCLIJSONArgs[processDeviceWatchControlResult](t, absoluteBinary,
		"device-watch-disable", "--state-dir", stateDir)
	if disabled.Changed || disabled.Active || disabled.State.Desired != "disabled" {
		t.Fatalf("disabled no-op returned unexpected state: %+v", disabled)
	}
	stopWithInterrupt(t, controller)

	assertNoEnabledDeviceWatchConfig(t, filepath.Join(stateDir, "config.json"))
	assertCapabilityIntentAuditCount(t, filepath.Join(stateDir, storage.Filename), 0)

	restarted := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, restarted)
	assertProcessDeviceWatchDisabled(t, absoluteBinary, stateDir, false)
	stopWithInterrupt(t, restarted)
}

func assertProcessDeviceWatchDisabled(t *testing.T, binary, stateDir string, wantConfigured bool) {
	t.Helper()
	list := runCLIJSONArgs[processCapabilityStateList](t, binary, "capabilities", "--state-dir", stateDir)
	for _, item := range list.Capabilities {
		if item.Manifest.ID != "device-watch" {
			continue
		}
		if item.Configured != wantConfigured || item.State.Desired != "disabled" || item.State.Verification != "unverified" {
			t.Fatalf("unexpected Device Watch capability state: %+v", item)
		}
		return
	}
	t.Fatal("Device Watch capability missing from process catalog")
}

func assertNoEnabledDeviceWatchConfig(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Capabilities []struct {
			ID      string `json:"id"`
			Desired string `json:"desired"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, item := range config.Capabilities {
		if item.ID == "device-watch" && item.Desired == "enabled" {
			t.Fatalf("unsupported Device Watch enable persisted in config: %s", data)
		}
	}
}

func assertCapabilityIntentAuditCount(t *testing.T, databasePath string, want int) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE kind = 'capability-intent'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("capability intent audit count = %d, want %d", count, want)
	}
}
