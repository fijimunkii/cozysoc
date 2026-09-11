package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/config"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const defaultTickInterval = 2 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr *os.File) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "dev":
		return runDev(ctx, args[1:], stdout, stderr)
	case "web":
		return runWeb(ctx, args[1:], stdout, stderr)
	case "coverage":
		return runCoverageCommand(ctx, args[1:], stdout, stderr)
	case "status":
		return runReadCommand(ctx, api.MethodStatus, args[1:], stdout, stderr)
	case "health":
		return runReadCommand(ctx, api.MethodHealth, args[1:], stdout, stderr)
	case "capabilities":
		return runReadCommand(ctx, api.MethodCapabilitiesList, args[1:], stdout, stderr)
	case "devices":
		return runReadCommand(ctx, api.MethodDevicesList, args[1:], stdout, stderr)
	case "networks":
		return runReadCommand(ctx, api.MethodNetworksList, args[1:], stdout, stderr)
	case "network-quality-plan":
		return runGatewayPlanCommand(ctx, args[1:], stdout, stderr)
	case "network-quality":
		return runReadCommand(ctx, api.MethodNetworkQualityLocal, args[1:], stdout, stderr)
	case "device-watch-coverage":
		return runReadCommand(ctx, api.MethodDeviceWatchCoverage, args[1:], stdout, stderr)
	case "device-watch-enable":
		return runReadCommand(ctx, api.MethodDeviceWatchEnable, args[1:], stdout, stderr)
	case "device-watch-disable":
		return runReadCommand(ctx, api.MethodDeviceWatchDisable, args[1:], stdout, stderr)
	case "device-label":
		return runDeviceLabelCommand(ctx, args[1:], stdout, stderr)
	case "network-enroll":
		return runNetworkEnrollCommand(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usageText())
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usageText())
	}
}

func usageError() error {
	return errors.New(usageText())
}

func usageText() string {
	return `cozysoc is the local Cozy SOC command-line entrypoint.

Usage:
  cozysoc serve [--state-dir PATH]
  cozysoc dev [--state-dir PATH] [--listen 127.0.0.1:PORT] [--ui-dir PATH]
  cozysoc web [--state-dir PATH] [--listen 127.0.0.1:PORT] [--ui-dir PATH]
  cozysoc coverage [--state-dir PATH]
  cozysoc status [--state-dir PATH]
  cozysoc health [--state-dir PATH]
  cozysoc capabilities [--state-dir PATH]
  cozysoc devices [--state-dir PATH]
  cozysoc networks [--state-dir PATH]
  cozysoc network-quality [--state-dir PATH]
  cozysoc network-quality-plan [--state-dir PATH] TARGET_IPV4
  cozysoc device-watch-coverage [--state-dir PATH]
  cozysoc device-watch-enable [--state-dir PATH]
  cozysoc device-watch-disable [--state-dir PATH]
  cozysoc device-label [--state-dir PATH] DEVICE_ID LABEL
  cozysoc network-enroll [--state-dir PATH] INTERFACE

The controller mode uses a permissioned Unix socket, requires a per-controller session secret, verifies OS peer identity on the current macOS and Linux reference paths, and keeps write operations explicitly allowlisted and controller-authorized.

The web mode is a separate local UI process. It does not own controller lifetime and does not expose the controller session secret or a generic controller-RPC endpoint.
`
}

func runServe(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("serve takes flags only")
	}

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}

	registry, err := capability.Builtins()
	if err != nil {
		return fmt.Errorf("load capability catalog: %w", err)
	}
	cfg, err := config.LoadOrCreate(dir)
	if err != nil {
		return err
	}
	configManager, err := config.NewManager(dir, registry, cfg)
	if err != nil {
		return fmt.Errorf("initialize controller configuration: %w", err)
	}
	instances, err := capability.NewInstances(registry, cfg.Capabilities)
	if err != nil {
		return fmt.Errorf("load capability instances: %w", err)
	}

	logger := newLogger(stderr, cfg.LogLevel)
	store, err := storage.Open(dir, storage.DefaultLimits())
	if err != nil {
		return fmt.Errorf("open controller storage: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("storage_close_failed")
		}
	}()

	ingestor, err := storage.NewIngestor(store, 0, logger)
	if err != nil {
		return fmt.Errorf("initialize storage ingestion: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := ingestor.Close(closeCtx); err != nil {
			logger.Warn("ingestion_close_failed")
		}
	}()

	deviceWatchDriver, err := devicewatch.NewLifecycleDriver(store, ingestor, logger)
	if err != nil {
		return fmt.Errorf("initialize Device Watch lifecycle: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := deviceWatchDriver.Close(closeCtx); err != nil {
			logger.Warn("device_watch_close_failed")
		}
	}()

	lifecycle, err := capability.NewLifecycleEngine(instances, map[string]capability.LifecycleDriver{
		devicewatch.CapabilityID: deviceWatchDriver,
	}, logger)
	if err != nil {
		return fmt.Errorf("initialize capability lifecycle: %w", err)
	}
	controller := core.New(buildVersion(), cfg.SchemaVersion, defaultTickInterval, lifecycle)
	controller.Start(ctx)

	deviceWatchControl, err := newDeviceWatchControl(configManager, lifecycle, deviceWatchDriver, store, logger)
	if err != nil {
		return err
	}
	if _, enabled, currentErr := deviceWatchControl.Current(); currentErr != nil {
		return currentErr
	} else if enabled {
		if _, reconcileErr := deviceWatchControl.Enable(ctx); reconcileErr != nil {
			logger.Warn("device_watch_not_started", "reason", deviceWatchStartupReason(reconcileErr))
		}
	}
	if err := startDeviceWatchVerification(ctx, lifecycle, logger); err != nil {
		return fmt.Errorf("start Device Watch verification: %w", err)
	}

	apiHandler, err := newControllerAPIHandler(controller, store, deviceWatchControl)
	if err != nil {
		return err
	}
	server, err := localapi.NewServer(dir, apiHandler, logger)
	if err != nil {
		return err
	}
	defer server.Close()

	// Only initialize run ownership AFTER acquiring the controller socket. A
	// rejected duplicate must not construct a second coordinator or reset limits.
	if err := apiHandler.startGatewayRuns(store); err != nil {
		return err
	}
	defer func() {
		// Cancellation is a request, not a join. Keep the audit store alive until
		// every admitted collaborator has returned. A broken collaborator must
		// leave shutdown pending, not write into storage that has already closed.
		if err := apiHandler.gatewayRuns.shutdown(context.Background()); err != nil {
			logger.Warn("gateway_runs_shutdown_failed")
		}
		logger.Info("gateway_runs_drained")
	}()

	logger.Info("controller_started",
		"version", controller.Version(),
		"api_version", api.Version,
		"config_schema_version", cfg.SchemaVersion,
		"capability_catalog_schema_version", capability.SchemaVersion,
		"socket", server.SocketPath(),
	)
	_, _ = fmt.Fprintf(stdout, "cozysoc controller ready: %s\n", server.SocketPath())

	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("controller_stopping")
	return nil
}

func deviceWatchStartupReason(err error) string {
	switch {
	case errors.Is(err, localapi.ErrMutationPrecondition):
		return "precondition-failed"
	case errors.Is(err, localapi.ErrMutationConflict):
		return "configuration-conflict"
	case errors.Is(err, devicewatch.ErrPlatformUnsupported):
		return "platform-unsupported"
	case errors.Is(err, storage.ErrNetworkScopeNotFound):
		return "scope-not-found"
	case errors.Is(err, devicewatch.ErrScopeMismatch):
		return "scope-mismatch"
	case errors.Is(err, devicewatch.ErrUnsupportedInterface):
		return "unsupported-interface"
	default:
		return "preflight-failed"
	}
}

func runReadCommand(ctx context.Context, method string, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet(method, flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s takes flags only", method)
	}

	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}

	client := localapi.NewClient(dir)
	result, err := client.Call(ctx, method)
	if err != nil {
		return err
	}

	var pretty any
	if err := json.Unmarshal(result, &pretty); err != nil {
		return fmt.Errorf("decode controller response: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pretty)
}

func resolveStateDir(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "cozysoc"), nil
}

func newLogger(w *os.File, level string) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slogLevel}))
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}
