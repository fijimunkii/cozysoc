package core

import (
	"testing"
	"time"
)

func TestRecordTickTracksFreshnessGap(t *testing.T) {
	controller := New("test", 1, time.Second)
	base := time.Now()
	controller.startedAt = base
	controller.lastTick = base

	controller.RecordTick(base.Add(2 * time.Second))
	if got := controller.Health(); got.State != "ok" || got.GapCount != 0 {
		t.Fatalf("unexpected healthy state: %+v", got)
	}

	controller.RecordTick(base.Add(6 * time.Second))
	got := controller.Health()
	if got.State != "degraded" || got.GapCount != 1 || got.LastGapAt == nil {
		t.Fatalf("gap not recorded: %+v", got)
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
