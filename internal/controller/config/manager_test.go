package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

func TestManagerPersistsAndRemovesCapabilityIntent(t *testing.T) {
	dir := t.TempDir()
	registry, err := capability.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	initial, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dir, registry, initial)
	if err != nil {
		t.Fatal(err)
	}
	rawScope, _ := json.Marshal("scope.home")
	enabled := capability.Configuration{
		ID: "device-watch", Ownership: capability.OwnershipBuiltin, Desired: capability.DesiredEnabled,
		Values: map[string]json.RawMessage{"network_scope_id": rawScope},
	}
	previous, changed, err := manager.ReplaceCapability("device-watch", &enabled)
	if err != nil || !changed || previous != nil {
		t.Fatalf("replace previous=%+v changed=%v err=%v", previous, changed, err)
	}

	reloaded, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Capabilities) != 1 || reloaded.Capabilities[0].Desired != capability.DesiredEnabled {
		t.Fatalf("persisted capability intent = %+v", reloaded.Capabilities)
	}

	got, ok := manager.Capability("device-watch")
	if !ok {
		t.Fatal("configured capability missing from manager")
	}
	got.Values["network_scope_id"][0] = 'x'
	again, _ := manager.Capability("device-watch")
	if string(again.Values["network_scope_id"]) != string(rawScope) {
		t.Fatal("manager returned mutable capability configuration")
	}

	previous, changed, err = manager.ReplaceCapability("device-watch", nil)
	if err != nil || !changed || previous == nil || previous.Desired != capability.DesiredEnabled {
		t.Fatalf("remove previous=%+v changed=%v err=%v", previous, changed, err)
	}
	reloaded, err = LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Capabilities) != 0 {
		t.Fatalf("removed capability persisted: %+v", reloaded.Capabilities)
	}
}

func TestManagerRejectsInvalidConfigurationWithoutChangingFile(t *testing.T) {
	dir := t.TempDir()
	registry, err := capability.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	initial, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dir, registry, initial)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Filename)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := capability.Configuration{
		ID: "device-watch", Ownership: capability.OwnershipBuiltin, Desired: capability.DesiredEnabled,
	}
	if _, _, err := manager.ReplaceCapability("device-watch", &invalid); err == nil {
		t.Fatal("invalid enabled capability configuration was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rejected capability configuration changed config file")
	}
}
