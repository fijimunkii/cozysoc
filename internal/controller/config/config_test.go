package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateCreatesSecureDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadOrCreate(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion || cfg.LogLevel != "info" {
		t.Fatalf("unexpected default config: %+v", cfg)
	}

	info, err := os.Stat(filepath.Join(dir, "state", Filename))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}

func TestInvalidConfigIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, Filename)
	original := []byte("{not-json}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreate(state); err == nil {
		t.Fatal("invalid config was accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid config was modified: %q", got)
	}
}

func TestFutureSchemaIsRejected(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, Filename)
	if err := os.WriteFile(path, []byte("{\"schema_version\":999,\"log_level\":\"info\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(state); err == nil {
		t.Fatal("future schema was accepted")
	}
}
