package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type diagnosticLister []capability.Instance

func (d diagnosticLister) List() []capability.Instance { return []capability.Instance(d) }

func TestDiagnosticPreviewExcludesHouseholdAndRawErrorFields(t *testing.T) {
	const private = "private-user@https://home.invalid/secret?token=abc"
	controller := core.New(private, 4, time.Second, diagnosticLister{
		{Manifest: capability.Manifest{ID: "device-watch", DisplayName: private, Summary: private}, State: capability.InstanceState{Desired: capability.DesiredEnabled, Verification: capability.VerificationDegraded}},
		{Manifest: capability.Manifest{ID: private, DisplayName: private}},
	})
	store := &fakeCoverageControllerStore{fakeDeviceStore: &fakeDeviceStore{}, err: errors.New(private)}
	control := &fakeDeviceWatchAPIControl{scopeID: private, configured: true}
	handler, err := newControllerAPIHandler(controller, store, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	handler.storageOverview = storageOverviewFixture{err: errors.New(private)}
	preview, err := handler.DiagnosticsPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.SchemaVersion != 2 || preview.Controller.BuildVersion != "redacted" || len(preview.Modules) != 1 || preview.Modules[0].ID != "device-watch" || len(preview.Coverage) != 1 || preview.Coverage[0].FailureCategory != "read-failed" || preview.Storage.ReadState != "unavailable" || preview.Storage.QuotaState != "unknown" || preview.Storage.VolumeState != "unknown" {
		t.Fatalf("diagnostic preview = %+v", preview)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{private, "home.invalid", "token=abc", "scope_id", "display_name", "summary", "sensor_id", "interface_name", "next_step", "database_bytes", "filesystem_available_bytes"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("diagnostic preview leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestDiagnosticStorageHealthUsesOnlyIndependentBoundedStates(t *testing.T) {
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), &fakeDeviceStore{}, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.storageOverview = storageOverviewFixture{health: storage.Health{
		QuotaState: storage.HealthPressure, FilesystemSupported: true,
		FilesystemState: storage.FilesystemCapacityFull, DatabaseBytes: 90,
		MaxBytes: 100, FilesystemAvailableBytes: 0,
	}}
	preview, err := handler.DiagnosticsPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Storage.ReadState != "current" || preview.Storage.QuotaState != "pressure" || preview.Storage.VolumeState != "full" {
		t.Fatalf("storage diagnostic = %+v", preview.Storage)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "database_bytes") || strings.Contains(string(encoded), "filesystem_available_bytes") {
		t.Fatalf("diagnostic leaked storage measurements: %s", encoded)
	}
	handler.storageOverview = storageOverviewFixture{health: storage.Health{
		QuotaState: storage.HealthState("/private/path"), FilesystemSupported: false,
		FilesystemState: storage.FilesystemCapacityFull,
	}}
	preview, err = handler.DiagnosticsPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Storage.QuotaState != "unknown" || preview.Storage.VolumeState != "unsupported" {
		t.Fatalf("invalid/unsupported storage diagnostic = %+v", preview.Storage)
	}
}

func TestDiagnosticCoverageFailureCategoriesAreBounded(t *testing.T) {
	for _, test := range []struct{ state, reason, want string }{
		{"active-limited", "fresh-limited", "none"}, {"degraded", "sensor-private-name", "sensor"},
		{"degraded", "ingestion-private-query", "ingestion"}, {"degraded", "storage-private-path", "storage"},
		{"stale", "stale-evidence", "evidence"}, {"degraded", "source-partial", "source"},
		{"degraded", "https://secret.invalid", "unknown"},
	} {
		if got := diagnosticCoverageFailure(test.state, test.reason); got != test.want {
			t.Fatalf("%s/%s = %s, want %s", test.state, test.reason, got, test.want)
		}
	}
}
