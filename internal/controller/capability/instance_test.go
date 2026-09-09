package capability

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstancesDefaultToDisabledAndUnverified(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	instances, err := NewInstances(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	instance, ok := instances.Get("device-watch")
	if !ok {
		t.Fatal("device-watch instance missing")
	}
	if instance.Configured {
		t.Fatal("implicit device-watch instance reported as explicitly configured")
	}
	if instance.Ownership != OwnershipBuiltin {
		t.Fatalf("unexpected ownership: %q", instance.Ownership)
	}
	if instance.State.Desired != DesiredDisabled || instance.State.Process != ProcessNotApplicable || instance.State.Verification != VerificationUnverified {
		t.Fatalf("unexpected default state: %+v", instance.State)
	}
}

func TestEnabledConfigurationRequiresTypedRequiredValues(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	missing := Configuration{ID: "device-watch", Ownership: OwnershipBuiltin, Desired: DesiredEnabled}
	if err := ValidateConfiguration(registry, missing); err == nil || !strings.Contains(err.Error(), "network_scope_id") {
		t.Fatalf("enabled config without required field accepted: %v", err)
	}

	wrongType := Configuration{
		ID:        "device-watch",
		Ownership: OwnershipBuiltin,
		Desired:   DesiredEnabled,
		Values:    map[string]json.RawMessage{"network_scope_id": json.RawMessage(`123`)},
	}
	if err := ValidateConfiguration(registry, wrongType); err == nil {
		t.Fatal("wrong config value type accepted")
	}

	nullValue := Configuration{
		ID:        "device-watch",
		Ownership: OwnershipBuiltin,
		Desired:   DesiredDisabled,
		Values:    map[string]json.RawMessage{"network_scope_id": json.RawMessage(`null`)},
	}
	if err := ValidateConfiguration(registry, nullValue); err == nil {
		t.Fatal("null configured value was accepted")
	}

	valid := Configuration{
		ID:        "device-watch",
		Ownership: OwnershipBuiltin,
		Desired:   DesiredEnabled,
		Values:    map[string]json.RawMessage{"network_scope_id": json.RawMessage(`"home-main"`)},
	}
	instances, err := NewInstances(registry, []Configuration{valid})
	if err != nil {
		t.Fatal(err)
	}
	instance, _ := instances.Get("device-watch")
	if !instance.Configured || instance.State.Desired != DesiredEnabled {
		t.Fatalf("explicit configuration not reflected in snapshot: %+v", instance)
	}
	if instance.State.Verification != VerificationUnverified {
		t.Fatal("persisted desired state must not restore verified evidence")
	}
}

func TestIntegerConfigurationRequiresNumericJSON(t *testing.T) {
	builtins, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	manifest, ok := builtins.Get("device-watch")
	if !ok {
		t.Fatal("device-watch builtin missing")
	}
	manifest.ID = "typed-test"
	manifest.Config.Fields = []ConfigField{
		{Name: "count", Type: ConfigInteger, Required: true, Description: "Test integer value."},
	}
	registry, err := NewRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}

	quoted := Configuration{
		ID:        "typed-test",
		Ownership: OwnershipBuiltin,
		Desired:   DesiredEnabled,
		Values:    map[string]json.RawMessage{"count": json.RawMessage(`"1"`)},
	}
	if err := ValidateConfiguration(registry, quoted); err == nil {
		t.Fatal("quoted numeric string was accepted as integer JSON")
	}

	numeric := quoted
	numeric.Values = map[string]json.RawMessage{"count": json.RawMessage(`1`)}
	if err := ValidateConfiguration(registry, numeric); err != nil {
		t.Fatalf("numeric integer JSON was rejected: %v", err)
	}
}

func TestConfigurationRejectsUnknownCapabilityOwnershipAndFields(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	cases := []Configuration{
		{ID: "missing", Ownership: OwnershipBuiltin, Desired: DesiredDisabled},
		{ID: "device-watch", Ownership: OwnershipExternal, Desired: DesiredDisabled},
		{ID: "device-watch", Ownership: OwnershipBuiltin, Desired: DesiredDisabled, Values: map[string]json.RawMessage{"command": json.RawMessage(`"sh"`)}},
	}
	for _, configuration := range cases {
		if err := ValidateConfiguration(registry, configuration); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", configuration)
		}
	}
}

func TestConfigurationValuesAreDefensivelyCopied(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	configuration := Configuration{
		ID:        "device-watch",
		Ownership: OwnershipBuiltin,
		Desired:   DesiredDisabled,
		Values:    map[string]json.RawMessage{"network_scope_id": json.RawMessage(`"home-main"`)},
	}
	instances, err := NewInstances(registry, []Configuration{configuration})
	if err != nil {
		t.Fatal(err)
	}
	configuration.Values["network_scope_id"][1] = 'X'

	stored := instances.configured["device-watch"].Values["network_scope_id"]
	if string(stored) != `"home-main"` {
		t.Fatalf("stored configuration mutated through caller buffer: %s", stored)
	}
}
