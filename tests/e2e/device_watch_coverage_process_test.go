package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type processDeviceWatchCoverage struct {
	Configured bool          `json:"configured"`
	ScopeID    string        `json:"scope_id,omitempty"`
	State      string        `json:"state"`
	Sources    []interface{} `json:"sources"`
	BlindSpots []interface{} `json:"blind_spots"`
	NextStep   string        `json:"next_step"`
}

func TestControllerProcessDeviceWatchCoverageDefaultState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process Device Watch coverage E2E currently targets the Linux CI reference runner")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc-controller binary to run process E2E", e2eBinaryEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	controller := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, controller)
	coverage := runCLIJSON[processDeviceWatchCoverage](t, absoluteBinary, stateDir, "device-watch-coverage")
	if coverage.Configured || coverage.ScopeID != "" || coverage.State != "unconfigured" {
		t.Fatalf("fresh controller exposed unexpected Device Watch coverage: %+v", coverage)
	}
	if coverage.Sources == nil || coverage.BlindSpots == nil || len(coverage.Sources) != 0 || len(coverage.BlindSpots) != 0 {
		t.Fatalf("unconfigured coverage returned unexpected detail collections: %+v", coverage)
	}
	if coverage.NextStep == "" {
		t.Fatal("unconfigured coverage did not provide an actionable next step")
	}
	stopWithInterrupt(t, controller)
}
