package e2e

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestWebProcessLocalQualityIsReadOnlyAndMinimized(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process boundary evidence, not hardware certification")
	}
	binary, uiDir := os.Getenv(e2eBinaryEnv), os.Getenv(e2eUIDirEnv)
	if binary == "" || uiDir == "" {
		t.Skip("set process E2E binary and built UI directory")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	uiDir, err = filepath.Abs(uiDir)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	controller := startController(t, binary, stateDir)
	waitForReady(t, binary, stateDir, controller)
	secret := readSecret(t, stateDir)
	web := startWeb(t, binary, stateDir, uiDir)
	root, origin, bootstrap := parseWebReadyURL(t, waitForWebReady(t, web))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar}
	response, err := client.Get(root + "api/network-quality")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("quality read did not require browser authentication")
	}
	r, err := http.NewRequest(http.MethodPost, root+"api/session", strings.NewReader(`{"bootstrap":"`+bootstrap+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	response, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("browser bootstrap failed")
	}
	read := func() api.LocalNetworkQuality {
		t.Helper()
		response, err := client.Get(root + "api/network-quality")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || len(body) > 8192 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("quality read failed: %s, %v", body, err)
		}
		for _, forbidden := range []string{secret, bootstrap, "scope_id", "sensor_id", "evidence_id", "prefixes", "summary", "next_step", "latency", "loss", "session"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("browser quality leaked %q", forbidden)
			}
		}
		var result api.LocalNetworkQuality
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	initial := read()
	if initial.Enrolled || initial.Check != nil || initial.Observer != nil {
		t.Fatal("unenrolled browser read invented a sample")
	}
	list := runCLIJSONArgs[processNetworkList](t, binary, "networks", "--state-dir", stateDir)
	if len(list.Candidates) == 0 {
		t.Fatal("no eligible Linux interface metadata")
	}
	candidate := list.Candidates[0]
	// Enrollment and sampling inspect OS metadata only; no traffic is sent.
	enrolled := runCLIJSONArgs[processNetworkEnrollResult](t, binary, "network-enroll", "--state-dir", stateDir, candidate.InterfaceName)
	for n := 0; n < 2; n++ {
		result := read()
		if !result.Enrolled || result.Observer == nil || result.Check == nil || result.Observer.InterfaceName != candidate.InterfaceName ||
			result.Check.AdministrativeUp == nil || !*result.Check.AdministrativeUp || result.Check.State != "check-succeeded" || result.Check.Source != "os-interface-metadata" ||
			!result.Check.FreshUntil.Equal(result.Check.CompletedAt.Add(30*time.Second)) {
			t.Fatalf("unexpected browser quality: %+v", result)
		}
	}
	response, err = client.Get(root + "api/network-quality?interface_name=lo")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("caller-selected interface was accepted")
	}
	coverage := runCLIJSONArgs[map[string]any](t, binary, "device-watch-coverage", "--state-dir", stateDir)
	if coverage["configured"] != false {
		t.Fatal("browser quality read enabled monitoring")
	}
	stopWithInterrupt(t, web)
	_ = runCLIJSONArgs[map[string]any](t, binary, "status", "--state-dir", stateDir)
	stopWithInterrupt(t, controller)
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
		t.Fatal("browser metadata read created monitoring history")
	}
}
