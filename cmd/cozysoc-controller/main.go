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

	"github.com/fijimunkii/cozysoc/internal/controller/config"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
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
		return runReadCommand(ctx, "status", args[1:], stdout, stderr)
	case "health":
		return runReadCommand(ctx, "health", args[1:], stdout, stderr)
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

The current v0.1 bootstrap API is read-only and uses a permissioned local Unix socket.
Platform peer-authentication and privileged helper boundaries remain owned by issue #8.
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

	cfg, err := config.LoadOrCreate(dir)
	if err != nil {
		return err
	}

	logger := newLogger(stderr, cfg.LogLevel)
	controller := core.New(buildVersion(), cfg.SchemaVersion, defaultTickInterval)
	controller.Start(ctx)

	server, err := localapi.NewServer(dir, controller, logger)
	if err != nil {
		return err
	}
	defer server.Close()

	logger.Info("controller_started",
		"version", controller.Version(),
		"api_version", 1,
		"config_schema_version", cfg.SchemaVersion,
		"socket", server.SocketPath(),
	)
	_, _ = fmt.Fprintf(stdout, "cozysoc controller ready: %s\n", server.SocketPath())

	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("controller_stopped")
	return nil
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

	client := localapi.NewClient(filepath.Join(dir, localapi.SocketFilename))
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
