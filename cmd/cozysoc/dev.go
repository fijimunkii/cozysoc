package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

const (
	devControllerProbeTimeout = 500 * time.Millisecond
	devControllerStartTimeout = 10 * time.Second
	devControllerStopTimeout  = 10 * time.Second
	devControllerPollInterval = 50 * time.Millisecond
	devControllerExitGrace    = time.Second
)

type devOptions struct {
	stateDir   string
	listenAddr string
	uiDir      string
}

type devControllerProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

func runDev(ctx context.Context, args []string, stdout, stderr *os.File) error {
	options, err := parseDevOptions(args, stderr)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "cozysoc dev: development-only orchestration; production controller lifetime is unchanged")

	if status, statusErr := devControllerStatus(ctx, options.stateDir); statusErr == nil {
		_, _ = fmt.Fprintf(stdout, "cozysoc dev: reusing existing controller pid %d\n", status.PID)
		return runWeb(ctx, options.webArgs(), stdout, stderr)
	} else if ctx.Err() != nil {
		return ctx.Err()
	}

	controller, err := startDevController(options.stateDir, stdout, stderr)
	if err != nil {
		return err
	}
	status, owned, err := waitForDevController(ctx, options.stateDir, controller)
	if err != nil {
		_ = controller.stop()
		return err
	}
	if !owned {
		_, _ = fmt.Fprintf(stdout, "cozysoc dev: reusing controller pid %d that became ready during startup\n", status.PID)
		return runWeb(ctx, options.webArgs(), stdout, stderr)
	}

	_, _ = fmt.Fprintf(stdout, "cozysoc dev: started temporary controller pid %d\n", status.PID)
	webErr := runWeb(ctx, options.webArgs(), stdout, stderr)
	stopErr := controller.stop()
	if ctx.Err() != nil {
		return webErr
	}
	if webErr != nil {
		return webErr
	}
	if stopErr != nil {
		return stopErr
	}
	_, _ = fmt.Fprintln(stdout, "cozysoc dev: stopped temporary controller")
	return nil
}

func parseDevOptions(args []string, stderr *os.File) (devOptions, error) {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	listenAddr := fs.String("listen", defaultWebListen, "loopback listen address")
	uiDir := fs.String("ui-dir", defaultWebUIDir, "built UI directory")
	if err := fs.Parse(args); err != nil {
		return devOptions{}, err
	}
	if fs.NArg() != 0 {
		return devOptions{}, fmt.Errorf("dev takes flags only")
	}
	if err := validateLoopbackListen(*listenAddr); err != nil {
		return devOptions{}, err
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return devOptions{}, err
	}
	assets, err := filepath.Abs(*uiDir)
	if err != nil {
		return devOptions{}, fmt.Errorf("resolve UI directory: %w", err)
	}
	assets, err = filepath.EvalSymlinks(assets)
	if err != nil {
		return devOptions{}, fmt.Errorf("resolve built UI directory symlinks: %w", err)
	}
	if err := validateUIDir(assets); err != nil {
		return devOptions{}, err
	}
	return devOptions{stateDir: dir, listenAddr: *listenAddr, uiDir: assets}, nil
}

func (o devOptions) webArgs() []string {
	return []string{"--state-dir", o.stateDir, "--listen", o.listenAddr, "--ui-dir", o.uiDir}
}

func devControllerStatus(ctx context.Context, stateDir string) (api.Status, error) {
	probeCtx, cancel := context.WithTimeout(ctx, devControllerProbeTimeout)
	defer cancel()
	result, err := localapi.NewClient(stateDir).Call(probeCtx, api.MethodStatus)
	if err != nil {
		return api.Status{}, err
	}
	var status api.Status
	if err := json.Unmarshal(result, &status); err != nil {
		return api.Status{}, fmt.Errorf("decode controller status: %w", err)
	}
	if status.PID <= 0 || status.Transport != "unix" {
		return api.Status{}, fmt.Errorf("controller status is not a valid local service")
	}
	return status, nil
}

func startDevController(stateDir string, stdout, stderr *os.File) (*devControllerProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve Cozy SOC executable: %w", err)
	}
	cmd := exec.Command(executable, "serve", "--state-dir", stateDir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start temporary dev controller: %w", err)
	}
	process := &devControllerProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		process.waitErr = cmd.Wait()
		close(process.done)
	}()
	return process, nil
}

func waitForDevController(ctx context.Context, stateDir string, process *devControllerProcess) (api.Status, bool, error) {
	timer := time.NewTimer(devControllerStartTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(devControllerPollInterval)
	defer ticker.Stop()
	done := process.done
	var exitedAt time.Time
	var exitedErr error

	for {
		status, err := devControllerStatus(ctx, stateDir)
		if err == nil {
			owned := status.PID == process.cmd.Process.Pid
			if !owned {
				_ = process.stop()
			}
			return status, owned, nil
		}
		if !exitedAt.IsZero() && time.Since(exitedAt) >= devControllerExitGrace {
			if exitedErr != nil {
				return api.Status{}, false, fmt.Errorf("temporary dev controller exited before readiness: %w", exitedErr)
			}
			return api.Status{}, false, fmt.Errorf("temporary dev controller exited before readiness")
		}

		select {
		case <-ctx.Done():
			_ = process.stop()
			return api.Status{}, false, ctx.Err()
		case <-done:
			exitedAt = time.Now()
			exitedErr = process.waitErr
			done = nil
		case <-ticker.C:
		case <-timer.C:
			_ = process.stop()
			return api.Status{}, false, fmt.Errorf("temporary dev controller did not become ready within %s", devControllerStartTimeout)
		}
	}
}

func (p *devControllerProcess) stop() error {
	select {
	case <-p.done:
		if p.waitErr != nil {
			return fmt.Errorf("temporary dev controller exited: %w", p.waitErr)
		}
		return nil
	default:
	}

	if err := p.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt temporary dev controller: %w", err)
	}
	timer := time.NewTimer(devControllerStopTimeout)
	defer timer.Stop()
	select {
	case <-p.done:
		if p.waitErr != nil {
			return fmt.Errorf("temporary dev controller stop failed: %w", p.waitErr)
		}
		return nil
	case <-timer.C:
		_ = p.cmd.Process.Kill()
		<-p.done
		return fmt.Errorf("temporary dev controller did not stop within %s", devControllerStopTimeout)
	}
}
