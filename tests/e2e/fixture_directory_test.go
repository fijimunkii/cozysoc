package e2e

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Short names leave room for the controller's Unix socket under TMPDIR.
// Own the cleanup binding here: a caller may reuse its returned path variable
// for an HTTP root URL without redirecting cleanup away from the fixture.
func shortProcessTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cz-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove process fixture directory: %v", err)
		}
	})
	return dir
}

func TestProcessFixtureDirectoryCleanupOwnsOriginalPath(t *testing.T) {
	var original string
	sentinel := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(sentinel, []byte("unrelated fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("caller-reuses-path", func(t *testing.T) {
		root := shortProcessTempDir(t)
		original = root
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			t.Fatal("fixture directory unavailable", err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
			t.Fatal("fixture directory is not private")
		}
		state := filepath.Join(root, "state ?#%")
		if err := os.Mkdir(state, 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(state, "fixture.db"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		// Later cleanup callbacks (controller/web/storage shutdown in real tests)
		// must run before the fixture directory is removed.
		t.Cleanup(func() {
			if _, err := os.Stat(state); err != nil {
				t.Error("fixture removed before resource cleanup", err)
			}
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		})
		root = "http://127.0.0.1:9000/"
		if root == original {
			t.Fatal("caller did not reuse path variable")
		}
	})
	if _, err := os.Lstat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("process fixture directory survived cleanup", err)
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "unrelated fixture" {
		t.Fatal("cleanup touched an unrelated fixture", err)
	}
}
