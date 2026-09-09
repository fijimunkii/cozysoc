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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
	return `cozysoc-controller is the local Cozy SOC controller process.

Usage:
  cozysoc-controller serve [--state-dir PATH]
  cozysoc-controller status [--state-dir PATH]
  cozysoc-controller health [--state-dir PATH]
  cozysoc-controller capabilities [--state-dir PATH]
  cozysoc-controller devices [--state-dir PATH]
  cozysoc-controller networks [--state-dir PATH]
  cozysoc-controller device-label [--state-dir PATH] DEVICE_ID LABEL
  cozysoc-controller network-enroll [--state-dir PATH] INTERFACE

The v0.1 local management API uses a permissioned Unix socket, requires a per-controller session secret, verifies OS peer identity on the current macOS and Linux reference paths, and keeps write operations explicitly allowlisted and controller-authorized.
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
	instances, err := capability.NewInstances(registry, cfg.Capabilities)
	if err != nil {
		return fmt.Errorf("load capability instances: %w", err)
	}

	logger := newLogger(stderr, cfg.LogLevel)
	lifecycle, err := capability.NewLifecycleEngine(instances, nil, logger)
	if err != nil {
		return fmt.Errorf("initialize capability lifecycle: %w", err)
	}
	controller := core.New(buildVersion(), cfg.SchemaVersion, defaultTickInterval, lifecycle)
	controller.Start(ctx)

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

	scopeID, deviceWatchEnabled, err := devicewatch.EnabledScopeID(cfg.Capabilities)
	if err != nil {
		return err
	}
	if deviceWatchEnabled {
		runtime, runtimeErr := devicewatch.NewRuntime(store, ingestor, logger)
		if runtimeErr != nil {
			logger.Warn("device_watch_not_started", "reason", deviceWatchStartupReason(runtimeErr))
		} else if runtimeErr = runtime.Start(ctx, scopeID); runtimeErr != nil {
			logger.Warn("device_watch_not_started", "reason", deviceWatchStartupReason(runtimeErr))
		}
	}

	apiHandler, err := newControllerAPIHandler(controller, store, scopeID, deviceWatchEnabled)
	if err != nil {
		return err
	}
	server, err := localapi.NewServer(dir, apiHandler, logger)
	if err != nil {
		return err
	}
	defer server.Close()

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
	logger.Info("controller_stopped")
	return nil
}

func deviceWatchStartupReason(err error) string {
	switch {
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
