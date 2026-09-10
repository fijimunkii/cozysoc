package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDevProcessStartsAndStopsTemporaryController(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("dev process E2E currently targets the Linux CI reference runner")
	}
	binary, uiDir := devE2EPaths(t)
	stateDir := filepath.Join(t.TempDir(), "state")

	dev := startDev(t, binary, stateDir, uiDir)
	waitForReady(t, binary, stateDir, dev)
	status := runCLIJSON[controllerStatus](t, binary, stateDir, "status")
	if status.PID == dev.cmd.Process.Pid {
		t.Fatalf("dev parent unexpectedly owns the controller process: %+v", status)
	}
	_ = waitForWebReady(t, dev)
	stdout, _ := os.ReadFile(dev.stdoutPath)
	if !strings.Contains(string(stdout), "cozysoc dev: development-only orchestration") || !strings.Contains(string(stdout), "cozysoc dev: started temporary controller pid") {
		t.Fatalf("dev did not clearly report temporary development ownership:\n%s", stdout)
	}

	stopWithInterrupt(t, dev)
	assertRemoved(t, filepath.Join(stateDir, "controller.sock"))
	assertRemoved(t, filepath.Join(stateDir, "controller.auth"))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "status", "--state-dir", stateDir).CombinedOutput()
	if err == nil {
		t.Fatalf("temporary dev controller survived dev exit: %s", output)
	}
}

func TestDevProcessReusesExistingControllerWithoutStoppingIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("dev process E2E currently targets the Linux CI reference runner")
	}
	binary, uiDir := devE2EPaths(t)
	stateDir := filepath.Join(t.TempDir(), "state")

	controller := startController(t, binary, stateDir)
	waitForReady(t, binary, stateDir, controller)
	secret := readSecret(t, stateDir)
	dev := startDev(t, binary, stateDir, uiDir)
	_ = waitForWebReady(t, dev)

	status := runCLIJSON[controllerStatus](t, binary, stateDir, "status")
	if status.PID != controller.cmd.Process.Pid {
		t.Fatalf("dev replaced the existing controller: status=%+v expected_pid=%d", status, controller.cmd.Process.Pid)
	}
	if got := readSecret(t, stateDir); got != secret {
		t.Fatal("dev rotated the existing controller session secret")
	}
	stdout, _ := os.ReadFile(dev.stdoutPath)
	if !strings.Contains(string(stdout), "cozysoc dev: reusing existing controller pid") {
		t.Fatalf("dev did not report existing-controller reuse:\n%s", stdout)
	}
	if strings.Contains(string(stdout), "cozysoc controller ready:") {
		t.Fatalf("dev appeared to start a duplicate controller while reuse was available:\n%s", stdout)
	}

	stopWithInterrupt(t, dev)
	status = runCLIJSON[controllerStatus](t, binary, stateDir, "status")
	if status.PID != controller.cmd.Process.Pid {
		t.Fatalf("stopping dev affected the reused controller: %+v", status)
	}
	if got := readSecret(t, stateDir); got != secret {
		t.Fatal("stopping dev changed the reused controller session secret")
	}
	stopWithInterrupt(t, controller)
}

func TestDevValidatesWebInputsBeforeStartingController(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("dev process E2E currently targets the Linux CI reference runner")
	}
	binary, _ := devE2EPaths(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	missingUI := filepath.Join(t.TempDir(), "missing-ui")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "dev", "--state-dir", stateDir, "--ui-dir", missingUI).CombinedOutput()
	if err == nil {
		t.Fatalf("dev unexpectedly accepted a missing UI directory: %s", output)
	}
	if !strings.Contains(string(output), "built UI") {
		t.Fatalf("dev returned an unexpected preflight error: %s", output)
	}
	assertRemoved(t, stateDir)
}

func devE2EPaths(t *testing.T) (string, string) {
	t.Helper()
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc binary to run process E2E", e2eBinaryEnv)
	}
	uiDir := os.Getenv(e2eUIDirEnv)
	if uiDir == "" {
		t.Skipf("set %s to a built UI directory to run dev process E2E", e2eUIDirEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	absoluteUI, err := filepath.Abs(uiDir)
	if err != nil {
		t.Fatal(err)
	}
	return absoluteBinary, absoluteUI
}

func startDev(t *testing.T, binary, stateDir, uiDir string) *runningController {
	t.Helper()
	logDir := t.TempDir()
	stdoutPath := filepath.Join(logDir, "dev-stdout.log")
	stderrPath := filepath.Join(logDir, "dev-stderr.log")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		_ = stdout.Close()
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "dev", "--state-dir", stateDir, "--listen", "127.0.0.1:0", "--ui-dir", uiDir)
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
	dev := &runningController{cmd: cmd, wait: wait, stdoutPath: stdoutPath, stderrPath: stderrPath}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return dev
}
