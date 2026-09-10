package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type processCoverageReport struct {
	CapabilityID      string        `json:"capability_id"`
	Configured        bool          `json:"configured"`
	State             string        `json:"state"`
	Reason            string        `json:"reason"`
	ObservationPoints []interface{} `json:"observation_points"`
	NextStep          string        `json:"next_step"`
}

type processDeviceWatchCoverage struct {
	Configured bool                   `json:"configured"`
	ScopeID    string                 `json:"scope_id,omitempty"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason,omitempty"`
	Coverage   *processCoverageReport `json:"coverage,omitempty"`
	Sources    []interface{}          `json:"sources"`
	BlindSpots []interface{}          `json:"blind_spots"`
	NextStep   string                 `json:"next_step"`
}

func TestControllerProcessDeviceWatchCoverageDefaultState(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process Device Watch coverage E2E currently targets the Linux CI reference runner")
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
	coverage := runCLIJSON[processDeviceWatchCoverage](t, absoluteBinary, stateDir, "device-watch-coverage")
	if coverage.Configured || coverage.ScopeID != "" || coverage.State != "unconfigured" {
		t.Fatalf("fresh controller exposed unexpected Device Watch coverage: %+v", coverage)
	}
	if coverage.Coverage == nil || coverage.Coverage.CapabilityID != "device-watch" || coverage.Coverage.Configured || coverage.Coverage.State != "unconfigured" || len(coverage.Coverage.ObservationPoints) != 0 {
		t.Fatalf("fresh controller exposed unexpected shared coverage contract: %+v", coverage.Coverage)
	}
	if coverage.State != coverage.Coverage.State || coverage.Reason != coverage.Coverage.Reason || coverage.NextStep != coverage.Coverage.NextStep {
		t.Fatalf("legacy/shared coverage state drift: top=%+v shared=%+v", coverage, coverage.Coverage)
	}
	if coverage.Sources == nil || coverage.BlindSpots == nil || len(coverage.Sources) != 0 || len(coverage.BlindSpots) != 0 {
		t.Fatalf("unconfigured coverage returned unexpected detail collections: %+v", coverage)
	}
	if coverage.NextStep == "" {
		t.Fatal("unconfigured coverage did not provide an actionable next step")
	}
	stopWithInterrupt(t, controller)
}
