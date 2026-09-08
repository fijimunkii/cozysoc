package core

import (
	"testing"
	"time"
)

func TestHealthDegradesWhenTickerIsStaleAndRecovers(t *testing.T) {
	controller := New("test", 1, time.Second)
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
	controller := New("v0.1-test", 1, time.Second)
	status := controller.Status()
	if status.APIVersion != 1 || status.ControllerVersion != "v0.1-test" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Transport != "unix" {
		t.Fatalf("unexpected transport: %q", status.Transport)
	}
}
