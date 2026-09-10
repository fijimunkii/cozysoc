package e2e

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/storage"
	_ "modernc.org/sqlite"
)

type processNetworkInterface struct {
	InterfaceName  string   `json:"interface_name"`
	InterfaceIndex int      `json:"interface_index"`
	Prefixes       []string `json:"prefixes"`
}

type processEnrolledNetwork struct {
	ScopeID   string                  `json:"scope_id"`
	Interface processNetworkInterface `json:"interface"`
}

type processNetworkList struct {
	Candidates []processNetworkInterface `json:"candidates"`
	Enrolled   *processEnrolledNetwork   `json:"enrolled,omitempty"`
}

type processNetworkEnrollResult struct {
	ScopeID   string                  `json:"scope_id"`
	Interface processNetworkInterface `json:"interface"`
	Changed   bool                    `json:"changed"`
}

func TestControllerProcessNetworkEnrollment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process network enrollment E2E currently targets the Linux CI reference runner")
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

	first := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, first)
	initial := runCLIJSONArgs[processNetworkList](t, absoluteBinary, "networks", "--state-dir", stateDir)
	if initial.Enrolled != nil {
		t.Fatalf("fresh controller unexpectedly has an enrolled network: %+v", initial.Enrolled)
	}
	if len(initial.Candidates) == 0 {
		t.Fatal("Linux CI runner exposed no eligible local interface candidates")
	}
	candidate := initial.Candidates[0]

	enrolled := runCLIJSONArgs[processNetworkEnrollResult](t, absoluteBinary,
		"network-enroll", "--state-dir", stateDir, candidate.InterfaceName)
	if !enrolled.Changed || enrolled.ScopeID == "" || enrolled.Interface.InterfaceName != candidate.InterfaceName {
		t.Fatalf("unexpected network enrollment result: %+v", enrolled)
	}
	assertProcessNetworkEnrollment(t, absoluteBinary, stateDir, enrolled.ScopeID, candidate.InterfaceName)

	noop := runCLIJSONArgs[processNetworkEnrollResult](t, absoluteBinary,
		"network-enroll", "--state-dir", stateDir, candidate.InterfaceName)
	if noop.Changed || noop.ScopeID != enrolled.ScopeID {
		t.Fatalf("idempotent network enrollment was not a no-op: %+v", noop)
	}

	assertCLIErrorContains(t, absoluteBinary, "precondition_failed",
		"network-enroll", "--state-dir", stateDir, "lo")
	if len(initial.Candidates) > 1 {
		assertCLIErrorContains(t, absoluteBinary, "conflict",
			"network-enroll", "--state-dir", stateDir, initial.Candidates[1].InterfaceName)
	}

	stopWithInterrupt(t, first)
	second := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, second)
	assertProcessNetworkEnrollment(t, absoluteBinary, stateDir, enrolled.ScopeID, candidate.InterfaceName)
	stopWithInterrupt(t, second)

	assertNetworkEnrollmentAudit(t, filepath.Join(stateDir, storage.Filename), enrolled.ScopeID, 1)
}

func assertProcessNetworkEnrollment(t *testing.T, binary, stateDir, scopeID, interfaceName string) {
	t.Helper()
	list := runCLIJSONArgs[processNetworkList](t, binary, "networks", "--state-dir", stateDir)
	if list.Enrolled == nil || list.Enrolled.ScopeID != scopeID || list.Enrolled.Interface.InterfaceName != interfaceName {
		t.Fatalf("unexpected enrolled network state: %+v", list.Enrolled)
	}
}

func assertNetworkEnrollmentAudit(t *testing.T, databasePath, scopeID string, want int) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events
		WHERE kind = 'network-scope-enroll' AND actor = 'local-os-user' AND json_extract(payload, '$.scope_id') = ?`, scopeID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("network enrollment audit count = %d, want %d", count, want)
	}
}
