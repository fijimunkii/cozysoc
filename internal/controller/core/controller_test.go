package core

import (
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

func TestHealthDegradesWhenTickerIsStaleAndRecovers(t *testing.T) {
	controller := New("test", 1, time.Second, nil)
	controller.lastTick = time.Now().Add(-4 * time.Second)

	stale := controller.Health()
	if stale.State != "degraded" {
		t.Fatalf("stale controller should be degraded: %+v", stale)
	}

	controller.RecordTick(time.Now())
	recovered := controller.Health()
	if recovered.State != "ok" || recovered.GapCount != 1 || recovered.LastGapAt == nil {
		t.Fatalf("controller did not recover while retaining gap history: %+v", recovered)
	}
}

func TestStatusSeparatesAPIVersionFromControllerVersion(t *testing.T) {
	controller := New("v0.1-test", 2, time.Second, nil)
	status := controller.Status()
	if status.APIVersion != 1 || status.ControllerVersion != "v0.1-test" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Transport != "unix" || status.ConfigSchemaVersion != 2 {
		t.Fatalf("unexpected status contract: %+v", status)
	}
}

func TestCapabilitiesExposeCatalogWithoutRestoringVerification(t *testing.T) {
	registry, err := capability.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	instances, err := capability.NewInstances(registry, []capability.Configuration{
		{ID: "device-watch", Ownership: capability.OwnershipBuiltin, Desired: capability.DesiredDisabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := New("test", 2, time.Second, instances)
	list := controller.Capabilities()
	if list.CatalogSchemaVersion != capability.SchemaVersion || len(list.Capabilities) != 1 {
		t.Fatalf("unexpected capability list: %+v", list)
	}
	if list.Capabilities[0].State.Verification != capability.VerificationUnverified {
		t.Fatalf("capability verification was restored from configuration: %+v", list.Capabilities[0])
	}
}
