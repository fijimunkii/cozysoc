package e2e

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestControllerProcessLocalNetworkQuality(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("local metadata process E2E currently targets Linux CI, not hardware certification")
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
	initial := runCLIJSONArgs[api.LocalNetworkQuality](t, binary, "network-quality", "--state-dir", stateDir)
	if initial.Enrolled || initial.Check != nil || initial.Observer != nil {
		t.Fatalf("fresh controller invented local quality evidence: %+v", initial)
	}
	assertCLIErrorContains(t, binary, "takes flags only", "network-quality", "--state-dir", stateDir, "lo")
	list := runCLIJSONArgs[processNetworkList](t, binary, "networks", "--state-dir", stateDir)
	if len(list.Candidates) == 0 {
		t.Fatal("Linux CI has no eligible interface for the local metadata check")
	}
	candidate := list.Candidates[0]
	// Enrollment and this read inspect local OS metadata only. No neighbor
	// collector or active gateway/resolver/external probe is enabled.
	enrolled := runCLIJSONArgs[processNetworkEnrollResult](t, binary, "network-enroll", "--state-dir", stateDir, candidate.InterfaceName)
	for read := 0; read < 2; read++ {
		result := runCLIJSONArgs[api.LocalNetworkQuality](t, binary, "network-quality", "--state-dir", stateDir)
		if !result.Enrolled || result.Observer == nil || result.Check == nil ||
			result.Observer.ScopeID != enrolled.ScopeID || result.Observer.InterfaceName != candidate.InterfaceName ||
			result.Check.AdministrativeUp == nil || !*result.Check.AdministrativeUp ||
			result.Check.State != "check-succeeded" || result.Check.Method != "interface-state" ||
			result.Check.Source != "os-interface-metadata" || result.Check.Confidence != "limited" ||
			result.Check.EvidenceID == "" || !result.Check.CompletedAt.Equal(result.AsOf) || len(result.Limitations) != 4 {
			t.Fatalf("unexpected live interface metadata: %+v", result)
		}
	}
	coverage := runCLIJSONArgs[map[string]any](t, binary, "device-watch-coverage", "--state-dir", stateDir)
	if coverage["configured"] != false {
		t.Fatal("quality read enabled Device Watch")
	}
	stopWithInterrupt(t, process)
	assertNetworkEnrollmentAudit(t, filepath.Join(stateDir, storage.Filename), enrolled.ScopeID, 1)
	db, err := sql.Open("sqlite", filepath.Join(stateDir, storage.Filename))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var observations, coverageSamples int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM observations), (SELECT count(*) FROM coverage_samples)`).Scan(&observations, &coverageSamples); err != nil {
		t.Fatal(err)
	}
	if observations != 0 || coverageSamples != 0 {
		t.Fatalf("metadata read created monitoring history: observations=%d coverage=%d", observations, coverageSamples)
	}
}
