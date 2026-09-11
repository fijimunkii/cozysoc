package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
)

func TestNativeGatewayExecutionRequiresExplicitOptIn(t *testing.T) {
	h, _, inspector := lifecycleHandler(t)
	audit := &lifecycleAuditor{}
	if err := h.startGatewayRuns(audit); err != nil {
		t.Fatal(err)
	}
	if c, err := h.GatewayCheckControl(); c != nil || err != gatewayrun.ErrUnavailable {
		t.Fatal("ordinary handler exposed execution")
	}
	if inspector.calls != 0 || audit.count() != 0 {
		t.Fatal("gate queried scope or wrote audit")
	}
	h.gatewayChecksEnabled = true
	first, err := h.GatewayCheckControl()
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, err := h.GatewayCheckControl()
	if err != nil || first != second {
		t.Fatal("native request replaced owner")
	}
	if err := h.gatewayRuns.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, err := h.GatewayCheckControl(); c != nil || err != gatewayrun.ErrUnavailable {
		t.Fatal("shutdown reopened execution")
	}
}

func TestUnsupportedGatewayOptInHasNoStartupSideEffects(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin opt-in rejection")
	}
	dir := filepath.Join(t.TempDir(), "state")
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := runServe(context.Background(), []string{"--state-dir", dir, "--experimental-gateway-checks"}, output, output); err == nil {
		t.Fatal("unsupported experimental startup accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unsupported opt-in mutated state", err)
	}
}
