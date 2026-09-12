package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

func TestNativeResolverExecutionRequiresExplicitOptIn(t *testing.T) {
	h, audit, calls := resolverHandler(t)
	if err := h.startResolverRuns(audit); err != nil {
		t.Fatal(err)
	}
	if c, err := h.ResolverCheckControl(); c != nil || err != resolverrun.ErrUnavailable {
		t.Fatal("ordinary handler exposed execution")
	}
	if *calls != 0 {
		t.Fatal("gate queried scope or wrote audit")
	}
	h.resolverChecksEnabled = true
	first, err := h.ResolverCheckControl()
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, err := h.ResolverCheckControl()
	if err != nil || first != second {
		t.Fatal("native request replaced owner")
	}
	if err := h.resolverRuns.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, err := h.ResolverCheckControl(); c != nil || err != resolverrun.ErrUnavailable {
		t.Fatal("shutdown reopened execution")
	}
}

func TestUnsupportedResolverOptInHasNoStartupSideEffects(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin opt-in rejection")
	}
	dir := filepath.Join(t.TempDir(), "state")
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := runServe(context.Background(), []string{"--state-dir", dir, "--experimental-resolver-checks"}, output, output); err == nil {
		t.Fatal("unsupported experimental startup accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unsupported opt-in mutated state", err)
	}
}
