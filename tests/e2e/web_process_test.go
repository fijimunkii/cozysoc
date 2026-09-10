package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const e2eUIDirEnv = "COZYSOC_E2E_UI_DIR"

type processCoverageEnvelope struct {
	AsOf    time.Time               `json:"as_of"`
	Reports []processCoverageReport `json:"reports"`
}

func TestWebProcessReadsCoverageWithoutOwningController(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("web process E2E currently targets the Linux CI reference runner")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc binary to run process E2E", e2eBinaryEnv)
	}
	uiDir := os.Getenv(e2eUIDirEnv)
	if uiDir == "" {
		t.Skipf("set %s to a built UI directory to run web process E2E", e2eUIDirEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	absoluteUI, err := filepath.Abs(uiDir)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	controller := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, controller)
	secret := readSecret(t, stateDir)

	cliCoverage := runCLIJSON[processCoverageEnvelope](t, absoluteBinary, stateDir, "coverage")
	if cliCoverage.AsOf.IsZero() || len(cliCoverage.Reports) != 1 || cliCoverage.Reports[0].CapabilityID != "device-watch" {
		t.Fatalf("unexpected generic CLI coverage: %+v", cliCoverage)
	}

	web := startWeb(t, absoluteBinary, stateDir, absoluteUI)
	baseURL := waitForWebReady(t, web)
	client := &http.Client{Timeout: 3 * time.Second}

	response, err := client.Get(baseURL + "api/coverage")
	if err != nil {
		t.Fatalf("read live web coverage: %v\n%s", err, web.logs())
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("coverage HTTP status = %d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), secret) {
		t.Fatal("web coverage response exposed the controller session secret")
	}
	for _, forbidden := range []string{"operational", "neighbors_in_scope", "blind_spots", "filesystem_available_bytes"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("web coverage response exposed Device Watch-only detail %q: %s", forbidden, body)
		}
	}
	var coverage processCoverageEnvelope
	if err := json.Unmarshal(body, &coverage); err != nil {
		t.Fatalf("decode web coverage: %v: %s", err, body)
	}
	if coverage.AsOf.IsZero() || len(coverage.Reports) != 1 {
		t.Fatalf("unexpected web coverage envelope: %+v", coverage)
	}
	report := coverage.Reports[0]
	if report.CapabilityID != "device-watch" || report.Configured || report.State != "unconfigured" || len(report.ObservationPoints) != 0 {
		t.Fatalf("unexpected shared web coverage report: %+v", report)
	}

	root, err := client.Get(baseURL)
	if err != nil {
		t.Fatalf("read web root: %v", err)
	}
	rootBody, err := io.ReadAll(root.Body)
	_ = root.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if root.StatusCode != http.StatusOK || !strings.Contains(string(rootBody), `id="root"`) {
		t.Fatalf("unexpected web root: status=%d body=%s", root.StatusCode, rootBody)
	}

	wrongHostRequest, err := http.NewRequest(http.MethodGet, baseURL+"api/coverage", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongHostRequest.Host = "attacker.invalid"
	wrongHost, err := client.Do(wrongHostRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, wrongHost.Body)
	_ = wrongHost.Body.Close()
	if wrongHost.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong Host status = %d, want 403", wrongHost.StatusCode)
	}

	stopWithInterrupt(t, web)
	status := runCLIJSON[controllerStatus](t, absoluteBinary, stateDir, "status")
	if status.PID != controller.cmd.Process.Pid || status.Transport != "unix" {
		t.Fatalf("stopping web affected controller: %+v", status)
	}
	stopWithInterrupt(t, controller)
}

func startWeb(t *testing.T, binary, stateDir, uiDir string) *runningController {
	t.Helper()
	logDir := t.TempDir()
	stdoutPath := filepath.Join(logDir, "web-stdout.log")
	stderrPath := filepath.Join(logDir, "web-stderr.log")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		_ = stdout.Close()
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "web", "--state-dir", stateDir, "--listen", "127.0.0.1:0", "--ui-dir", uiDir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		wait <- err
		close(wait)
	}()
	web := &runningController{cmd: cmd, wait: wait, stdoutPath: stdoutPath, stderrPath: stderrPath}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return web
}

func waitForWebReady(t *testing.T, web *runningController) string {
	t.Helper()
	const prefix = "cozysoc web ready: "
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-web.wait:
			t.Fatalf("web process exited before readiness: %v\n%s", err, web.logs())
		default:
		}
		stdout, _ := os.ReadFile(web.stdoutPath)
		for _, line := range strings.Split(string(stdout), "\n") {
			if strings.HasPrefix(line, prefix) {
				url := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
					t.Fatalf("unexpected web readiness URL %q", url)
				}
				return url
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("web process did not become ready\n%s", web.logs())
	return fmt.Sprintf("http://127.0.0.1:%d/", 0)
}

func runCommandJSON[T any](t *testing.T, binary string, args ...string) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("command %v failed: %v: %s", args, err, output)
	}
	var value T
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("decode command %v: %v: %s", args, err, output)
	}
	return value
}
