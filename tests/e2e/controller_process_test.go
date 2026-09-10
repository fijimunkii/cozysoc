package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const e2eBinaryEnv = "COZYSOC_E2E_BINARY"

type controllerStatus struct {
	APIVersion        int    `json:"api_version"`
	ControllerVersion string `json:"controller_version"`
	PID               int    `json:"pid"`
	Transport         string `json:"transport"`
}

type controllerHealth struct {
	State string `json:"state"`
}

type capabilityList struct {
	Capabilities []struct {
		Manifest struct {
			ID string `json:"id"`
		} `json:"manifest"`
	} `json:"capabilities"`
}

type deviceList struct {
	Configured bool              `json:"configured"`
	ScopeID    string            `json:"scope_id,omitempty"`
	Devices    []json.RawMessage `json:"devices"`
	Truncated  bool              `json:"truncated"`
}

type runningController struct {
	cmd        *exec.Cmd
	wait       chan error
	stdoutPath string
	stderrPath string
}

func TestControllerProcessSmoke(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process smoke test currently targets the Linux CI reference runner")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc binary to run process E2E", e2eBinaryEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(absoluteBinary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("E2E binary is unavailable at %q: %v", absoluteBinary, err)
	}

	stateDir := filepath.Join(t.TempDir(), "state")

	first := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, first)
	assertRunningSurface(t, absoluteBinary, stateDir, first.cmd.Process.Pid)
	assertPrivateState(t, stateDir)
	secret1 := readSecret(t, stateDir)
	assertSecondInstanceRejected(t, absoluteBinary, stateDir, secret1)

	stopWithInterrupt(t, first)
	assertRemoved(t, filepath.Join(stateDir, "controller.sock"))
	assertRemoved(t, filepath.Join(stateDir, "controller.auth"))

	second := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, second)
	secret2 := readSecret(t, stateDir)
	if secret2 == secret1 {
		t.Fatal("controller session secret did not rotate after clean restart")
	}
	assertRunningSurface(t, absoluteBinary, stateDir, second.cmd.Process.Pid)

	killController(t, second)

	third := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, third)
	secret3 := readSecret(t, stateDir)
	if secret3 == secret2 {
		t.Fatal("controller session secret did not rotate after crash recovery")
	}
	assertRunningSurface(t, absoluteBinary, stateDir, third.cmd.Process.Pid)
	stopWithInterrupt(t, third)

	if info, err := os.Stat(filepath.Join(stateDir, "cozysoc.db")); err != nil || info.Size() == 0 {
		t.Fatalf("controller database was not preserved across restarts: info=%v err=%v", info, err)
	}
}

func startController(t *testing.T, binary, stateDir string) *runningController {
	t.Helper()
	logDir := t.TempDir()
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		_ = stdout.Close()
		t.Fatal(err)
	}

	cmd := exec.Command(binary, "serve", "--state-dir", stateDir)
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
	controller := &runningController{cmd: cmd, wait: wait, stdoutPath: stdoutPath, stderrPath: stderrPath}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return controller
}

func waitForReady(t *testing.T, binary, stateDir string, controller *runningController) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-controller.wait:
			t.Fatalf("controller exited before readiness: %v\n%s", err, controller.logs())
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		cmd := exec.CommandContext(ctx, binary, "status", "--state-dir", stateDir)
		_, lastErr = cmd.CombinedOutput()
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("controller did not become ready: %v\n%s", lastErr, controller.logs())
}

func assertRunningSurface(t *testing.T, binary, stateDir string, expectedPID int) {
	t.Helper()
	status := runCLIJSON[controllerStatus](t, binary, stateDir, "status")
	if status.APIVersion != 1 || status.Transport != "unix" || status.PID != expectedPID {
		t.Fatalf("unexpected process status: %+v expected_pid=%d", status, expectedPID)
	}

	health := runCLIJSON[controllerHealth](t, binary, stateDir, "health")
	if health.State != "ok" {
		t.Fatalf("unexpected controller health: %+v", health)
	}

	capabilities := runCLIJSON[capabilityList](t, binary, stateDir, "capabilities")
	foundDeviceWatch := false
	for _, capability := range capabilities.Capabilities {
		if capability.Manifest.ID == "device-watch" {
			foundDeviceWatch = true
			break
		}
	}
	if !foundDeviceWatch {
		t.Fatal("built-in Device Watch capability is missing from the process API")
	}

	devices := runCLIJSON[deviceList](t, binary, stateDir, "devices")
	if devices.Configured || devices.ScopeID != "" || len(devices.Devices) != 0 || devices.Truncated {
		t.Fatalf("fresh default controller exposed unexpected Device Watch state: %+v", devices)
	}
}

func assertPrivateState(t *testing.T, stateDir string) {
	t.Helper()
	assertMode(t, stateDir, 0o700)
	assertMode(t, filepath.Join(stateDir, "controller.sock"), 0o600)
	assertMode(t, filepath.Join(stateDir, "controller.auth"), 0o600)
	assertMode(t, filepath.Join(stateDir, "config.json"), 0o600)
	assertMode(t, filepath.Join(stateDir, "cozysoc.db"), 0o600)
}

func assertSecondInstanceRejected(t *testing.T, binary, stateDir, activeSecret string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "serve", "--state-dir", stateDir).CombinedOutput()
	if err == nil {
		t.Fatalf("second controller unexpectedly started: %s", output)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("second controller did not fail closed within deadline: %s", output)
	}
	if !strings.Contains(string(output), "already running") {
		t.Fatalf("second controller failed for an unexpected reason: %s", output)
	}
	if got := readSecret(t, stateDir); got != activeSecret {
		t.Fatal("failed second controller rotated the active session secret")
	}
}

func stopWithInterrupt(t *testing.T, controller *runningController) {
	t.Helper()
	if err := controller.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal controller: %v\n%s", err, controller.logs())
	}
	select {
	case err := <-controller.wait:
		if err != nil {
			t.Fatalf("controller did not stop cleanly: %v\n%s", err, controller.logs())
		}
	case <-time.After(5 * time.Second):
		_ = controller.cmd.Process.Kill()
		t.Fatalf("controller did not stop after interrupt\n%s", controller.logs())
	}
}

func killController(t *testing.T, controller *runningController) {
	t.Helper()
	if err := controller.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill controller: %v", err)
	}
	select {
	case err := <-controller.wait:
		if err == nil {
			t.Fatal("killed controller unexpectedly exited successfully")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("killed controller was not reaped\n%s", controller.logs())
	}
}

func runCLIJSON[T any](t *testing.T, binary, stateDir, command string) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, command, "--state-dir", stateDir).CombinedOutput()
	if err != nil {
		t.Fatalf("%s command failed: %v: %s", command, err, output)
	}
	var value T
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("decode %s output: %v: %s", command, err, output)
	}
	return value
}

func readSecret(t *testing.T, stateDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, "controller.auth"))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		t.Fatal("controller session secret is empty")
	}
	return secret
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions = %o, want %o", path, got, want)
	}
}

func assertRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be removed after clean shutdown, err=%v", path, err)
	}
}

func (c *runningController) logs() string {
	stdout, _ := os.ReadFile(c.stdoutPath)
	stderr, _ := os.ReadFile(c.stderrPath)
	return fmt.Sprintf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
}
