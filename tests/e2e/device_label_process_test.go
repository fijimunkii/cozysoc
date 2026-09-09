package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
	_ "modernc.org/sqlite"
)

type processDeviceList struct {
	Configured bool   `json:"configured"`
	ScopeID    string `json:"scope_id"`
	Devices    []struct {
		ID        string `json:"id"`
		UserLabel string `json:"user_label,omitempty"`
		State     string `json:"state"`
	} `json:"devices"`
}

type processDeviceLabelResult struct {
	DeviceID  string `json:"device_id"`
	UserLabel string `json:"user_label,omitempty"`
	Changed   bool   `json:"changed"`
}

func TestControllerProcessDeviceLabelMutation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process mutation E2E currently targets the Linux CI reference runner")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc-controller binary to run process E2E", e2eBinaryEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	seedDeviceLabelFixture(t, stateDir)

	first := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, first)
	assertFixtureDeviceLabel(t, absoluteBinary, stateDir, "")

	set := runCLIJSONArgs[processDeviceLabelResult](t, absoluteBinary,
		"device-label", "--state-dir", stateDir, "device.e2e", "Living Room TV")
	if !set.Changed || set.DeviceID != "device.e2e" || set.UserLabel != "Living Room TV" {
		t.Fatalf("unexpected label set result: %+v", set)
	}
	assertFixtureDeviceLabel(t, absoluteBinary, stateDir, "Living Room TV")

	stopWithInterrupt(t, first)
	second := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, second)
	assertFixtureDeviceLabel(t, absoluteBinary, stateDir, "Living Room TV")

	noop := runCLIJSONArgs[processDeviceLabelResult](t, absoluteBinary,
		"device-label", "--state-dir", stateDir, "device.e2e", "Living Room TV")
	if noop.Changed || noop.UserLabel != "Living Room TV" {
		t.Fatalf("idempotent label was not a no-op: %+v", noop)
	}

	assertCLIErrorContains(t, absoluteBinary, "invalid_request",
		"device-label", "--state-dir", stateDir, "device.e2e", strings.Repeat("x", storage.MaxDeviceLabelBytes+1))
	assertCLIErrorContains(t, absoluteBinary, "not_found",
		"device-label", "--state-dir", stateDir, "device.unknown", "Unknown")

	cleared := runCLIJSONArgs[processDeviceLabelResult](t, absoluteBinary,
		"device-label", "--state-dir", stateDir, "device.e2e", "")
	if !cleared.Changed || cleared.UserLabel != "" {
		t.Fatalf("unexpected label clear result: %+v", cleared)
	}
	assertFixtureDeviceLabel(t, absoluteBinary, stateDir, "")
	stopWithInterrupt(t, second)

	assertDeviceLabelAudit(t, filepath.Join(stateDir, storage.Filename), "device.e2e", 2)
}

func seedDeviceLabelFixture(t *testing.T, stateDir string) {
	t.Helper()
	store, err := storage.Open(stateDir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	confidence := 0.9
	validUntil := now.Add(10 * time.Minute)

	for _, operation := range []func() error{
		func() error {
			return store.CreateNetworkScope(ctx, domain.NetworkScope{
				ID: "scope.e2e", Kind: "lan", EnrolledAt: now, Metadata: json.RawMessage(`{"fixture":true}`),
			})
		},
		func() error {
			return store.CreateSensor(ctx, domain.Sensor{
				ID: "sensor.e2e", ScopeID: "scope.e2e", Kind: "fixture", Ownership: "builtin",
				RegisteredAt: now, Metadata: json.RawMessage(`{"fixture":true}`),
			})
		},
		func() error { return store.CreateDevice(ctx, domain.Device{ID: "device.e2e", CreatedAt: now}) },
		func() error {
			return store.InsertIdentityClaim(ctx, domain.IdentityClaim{
				ID: "claim.e2e", ScopeID: "scope.e2e", Kind: domain.ClaimMAC, Value: "02:00:00:00:00:42",
				ObservedAt: now, ValidUntil: &validUntil, Confidence: &confidence, SourceSensorID: "sensor.e2e",
				Retention: domain.RetentionStandard,
			})
		},
		func() error {
			return store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
				ID: "link.e2e", DeviceID: "device.e2e", ClaimID: "claim.e2e", ValidFrom: now,
				ValidUntil: &validUntil, Confidence: &confidence, Authority: domain.LinkInferred,
				Reason: "e2e-fixture", CreatedAt: now,
			})
		},
	} {
		if err := operation(); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	config := map[string]any{
		"schema_version": 2,
		"log_level":      "info",
		"capabilities": []any{
			map[string]any{
				"id":        "device-watch",
				"ownership": "builtin",
				"desired":   "enabled",
				"values": map[string]any{
					"network_scope_id": "scope.e2e",
				},
			},
		},
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFixtureDeviceLabel(t *testing.T, binary, stateDir, want string) {
	t.Helper()
	list := runCLIJSONArgs[processDeviceList](t, binary, "devices", "--state-dir", stateDir)
	if !list.Configured || list.ScopeID != "scope.e2e" || len(list.Devices) != 1 {
		t.Fatalf("unexpected fixture devices: %+v", list)
	}
	device := list.Devices[0]
	if device.ID != "device.e2e" || device.UserLabel != want {
		t.Fatalf("fixture device label = %q, want %q: %+v", device.UserLabel, want, device)
	}
}

func runCLIJSONArgs[T any](t *testing.T, binary string, args ...string) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("command %v failed: %v: %s", args, err, output)
	}
	var value T
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("decode command %v output: %v: %s", args, err, output)
	}
	return value
}

func assertCLIErrorContains(t *testing.T, binary, want string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err == nil {
		t.Fatalf("command %v unexpectedly succeeded: %s", args, output)
	}
	if !strings.Contains(string(output), want) {
		t.Fatalf("command %v error does not contain %q: %s", args, want, output)
	}
}

func assertDeviceLabelAudit(t *testing.T, databasePath, deviceID string, want int) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events
		WHERE kind = 'device-label' AND actor = 'local-os-user' AND json_extract(payload, '$.device_id') = ?`, deviceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("device label audit count = %d, want %d", count, want)
	}
}
