package capability

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuiltinsIncludeDeviceWatchWithoutSupportInflation(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	manifest, ok := registry.Get("device-watch")
	if !ok {
		t.Fatal("device-watch builtin missing")
	}
	if len(manifest.Targets) != 1 || manifest.Targets[0].Support != SupportCandidate {
		t.Fatalf("device-watch support must remain candidate: %+v", manifest.Targets)
	}
	if manifest.Resources.Measurement != MeasurementUnmeasured {
		t.Fatalf("unmeasured resource budget was inflated: %+v", manifest.Resources)
	}
	if len(manifest.DeepLinks) != 0 {
		t.Fatalf("unexpected deep links: %+v", manifest.DeepLinks)
	}
}

func TestDecodeRejectsUnknownFieldsAndOversizedManifests(t *testing.T) {
	data := `{"schema_version":1,"id":"test","display_name":"Test","summary":"Test capability","release":"v0.1","ownership":["builtin"],"targets":[{"os":"darwin","arch":"arm64","support":"candidate"}],"inputs":[],"privileges":[],"dependencies":[],"config":{"schema_version":1,"fields":[]},"resources":{"measurement":"unmeasured","profile":"base"},"provenance":{"kind":"first-party","license":"MIT","source":"repo","version_policy":"controller"},"health":{"process_required":false,"verification_signals":["freshness"],"coverage_requires_verification":true},"outputs":[{"kind":"observation","schema_version":1}],"deep_links":[],"lifecycle":["preflight","verify"],"command":"sh"}`
	if _, err := Decode(strings.NewReader(data)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown executable-shaped field accepted: %v", err)
	}
	if _, err := Decode(bytes.NewReader(make([]byte, MaxManifestBytes+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized manifest accepted: %v", err)
	}
}

func TestRegistryRejectsDuplicatesAndReturnsCopies(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	original, _ := registry.Get("device-watch")
	if _, err := NewRegistry(original, original); err == nil {
		t.Fatal("duplicate capability accepted")
	}

	copyOne, _ := registry.Get("device-watch")
	copyOne.Config.Fields[0].Name = "mutated"
	copyTwo, _ := registry.Get("device-watch")
	if copyTwo.Config.Fields[0].Name == "mutated" {
		t.Fatal("registry leaked mutable manifest state")
	}
}

func TestMatchTargetChecksPlatformAndMinimumVersion(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := registry.Get("device-watch")

	matched := MatchTarget(manifest, Environment{OS: "darwin", Arch: "arm64", OSVersion: "14.2"})
	if !matched.Matched || matched.Target.Support != SupportCandidate {
		t.Fatalf("expected candidate target match: %+v", matched)
	}
	tooOld := MatchTarget(manifest, Environment{OS: "darwin", Arch: "arm64", OSVersion: "12.7"})
	if tooOld.Matched || !strings.Contains(tooOld.Reason, "13.0") {
		t.Fatalf("old macOS unexpectedly matched: %+v", tooOld)
	}
	wrongArch := MatchTarget(manifest, Environment{OS: "darwin", Arch: "amd64", OSVersion: "14.2"})
	if wrongArch.Matched {
		t.Fatalf("unlisted architecture unexpectedly matched: %+v", wrongArch)
	}
}

func TestInstanceStateSeparatesProcessFromVerification(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := registry.Get("device-watch")

	state := InstanceState{Desired: DesiredEnabled, Process: ProcessNotApplicable, Verification: VerificationUnverified}
	if err := state.Validate(manifest); err != nil {
		t.Fatal(err)
	}
	if state.Verification == VerificationVerified {
		t.Fatal("enabled state must not imply verified coverage")
	}

	bad := state
	bad.Process = ProcessRunning
	if err := bad.Validate(manifest); err == nil {
		t.Fatal("builtin capability accepted fake independent process state")
	}
}

func TestMeasuredResourceBudgetRequiresEvidence(t *testing.T) {
	registry, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := registry.Get("device-watch")
	manifest.Resources = ResourceBudget{Measurement: MeasurementMeasured, Profile: "desktop-base", MaxRAMMiB: 64, MaxDiskMiB: 128, MaxCPUPercent: 5}
	if err := Validate(manifest); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("measured budget without evidence accepted: %v", err)
	}
}
