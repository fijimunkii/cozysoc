package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
)

func TestNativeHTTPSExecutionRequiresExplicitOptIn(t *testing.T) {
	h, audit, _ := httpsReviewHandler(t)
	if err := h.startHTTPSRuns(audit); err != nil {
		t.Fatal(err)
	}
	if c, err := h.HTTPSCheckControl(); c != nil || err != httpsrun.ErrUnavailable {
		t.Fatal("ordinary handler exposed execution")
	}
	h.httpsChecksEnabled = true
	first, err := h.HTTPSCheckControl()
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, err := h.HTTPSCheckControl()
	if err != nil || first != second {
		t.Fatal("native request replaced owner")
	}
	if err := h.httpsRuns.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, err := h.HTTPSCheckControl(); c != nil || err != httpsrun.ErrUnavailable {
		t.Fatal("shutdown reopened execution")
	}
}

func TestUnsupportedHTTPSOptInHasNoStartupSideEffects(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin opt-in rejection")
	}
	dir := filepath.Join(t.TempDir(), "state")
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := runServe(context.Background(), []string{"--state-dir", dir, "--experimental-https-checks"}, output, output); err == nil {
		t.Fatal("unsupported experimental startup accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unsupported opt-in mutated state", err)
	}
}
