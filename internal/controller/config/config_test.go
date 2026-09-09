package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateCreatesSecureDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadOrCreate(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion || cfg.LogLevel != "info" || len(cfg.Capabilities) != 0 {
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

func TestSchemaOneMigratesAtomicallyToCurrentSchema(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, Filename)
	if err := os.WriteFile(path, []byte("{\"schema_version\":1,\"log_level\":\"warn\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadOrCreate(state)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion || cfg.LogLevel != "warn" {
		t.Fatalf("unexpected migrated config: %+v", cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema migration was not persisted: %s", data)
	}
}

func TestCapabilityIntentLoadsFromCurrentConfig(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, Filename)
	original := []byte(`{
  "schema_version": 2,
  "log_level": "info",
  "capabilities": [
    {
      "id": "device-watch",
      "ownership": "builtin",
      "desired": "disabled",
      "values": {"network_scope_id": "home-main"}
    }
  ]
}
`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadOrCreate(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Capabilities) != 1 || cfg.Capabilities[0].ID != "device-watch" {
		t.Fatalf("capability intent not loaded: %+v", cfg.Capabilities)
	}
	if got := string(cfg.Capabilities[0].Values["network_scope_id"]); got != `"home-main"` {
		t.Fatalf("unexpected configured value: %s", got)
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

func TestUnknownFieldsAreRejectedWithoutOverwrite(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, Filename)
	original := []byte("{\"schema_version\":2,\"log_level\":\"info\",\"command\":\"sh\"}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(state); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown config field was accepted: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("rejected config was overwritten")
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

func TestOversizedConfigIsRejected(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, Filename)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(state); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized config was accepted: %v", err)
	}
}
