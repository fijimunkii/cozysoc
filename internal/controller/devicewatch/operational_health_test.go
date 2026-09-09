package devicewatch

import (
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestSensorHealthDistinguishesCurrentStaleFailureAndDisconnection(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	tests := []struct {
		name    string
		runtime operationalRuntime
		want    OperationalState
	}{
		{name: "unavailable", runtime: nil, want: OperationalUnavailable},
		{name: "disconnected", runtime: &fakeRuntimeControl{}, want: OperationalDisconnected},
		{name: "starting", runtime: &fakeRuntimeControl{running: true}, want: OperationalStarting},
		{name: "failed collection", runtime: &fakeRuntimeControl{running: true, state: RuntimeState{LastAttemptAt: now, LastSuccessfulAt: now.Add(-time.Minute), LastErrorClass: "source-unavailable"}}, want: OperationalDegraded},
		{name: "stale", runtime: &fakeRuntimeControl{running: true, state: RuntimeState{LastAttemptAt: now.Add(-4 * time.Minute), LastSuccessfulAt: now.Add(-4 * time.Minute)}}, want: OperationalStale},
		{name: "current", runtime: &fakeRuntimeControl{running: true, state: RuntimeState{LastAttemptAt: now.Add(-time.Minute), LastSuccessfulAt: now.Add(-time.Minute)}}, want: OperationalCurrent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sensorHealth(test.runtime, now)
			if got.State != test.want {
				t.Fatalf("sensor health = %+v, want %s", got, test.want)
			}
		})
	}
}

func TestPipelineHealthSeparatesCurrentPressureAndFailure(t *testing.T) {
	tests := []struct {
		state storage.IngestionHealthState
		want  OperationalState
	}{
		{state: storage.IngestionHealthCurrent, want: OperationalCurrent},
		{state: storage.IngestionHealthPressure, want: OperationalDegraded},
		{state: storage.IngestionHealthBackpressure, want: OperationalDegraded},
		{state: storage.IngestionHealthWriteFailed, want: OperationalDegraded},
		{state: storage.IngestionHealthClosed, want: OperationalDisconnected},
	}
	for _, test := range tests {
		got := pipelineHealth(storage.IngestionHealth{State: test.state, Capacity: 8, Depth: 2, Dropped: 3, Failed: 4})
		if got.State != test.want || got.Dropped != 3 || got.Failed != 4 {
			t.Fatalf("pipeline health for %s = %+v", test.state, got)
		}
	}
}

func TestEffectiveCoverageMakesOperationalFailurePrimary(t *testing.T) {
	report := CoverageReport{
		State:    CoverageActiveLimited,
		Reason:   "fresh-limited",
		NextStep: "Use Traffic Watch.",
	}
	health := OperationalHealth{
		Sensor:   SensorHealth{State: OperationalCurrent},
		Pipeline: PipelineHealth{State: OperationalCurrent},
		Database: DatabaseHealth{State: OperationalCurrent},
	}
	state, reason, _ := EffectiveCoverage(report, health)
	if state != CoverageActiveLimited || reason != "fresh-limited" {
		t.Fatalf("healthy effective coverage = %s %s", state, reason)
	}

	health.Pipeline = PipelineHealth{State: OperationalDegraded, Reason: "backpressure", NextStep: "Restore throughput."}
	state, reason, next := EffectiveCoverage(report, health)
	if state != CoverageDegraded || reason != "ingestion-backpressure" || next != "Restore throughput." {
		t.Fatalf("pipeline effective coverage = %s %s %q", state, reason, next)
	}

	health.Sensor = SensorHealth{State: OperationalDisconnected, NextStep: "Restart Device Watch."}
	state, reason, next = EffectiveCoverage(report, health)
	if state != CoverageDisconnected || reason != "sensor-disconnected" || next != "Restart Device Watch." {
		t.Fatalf("disconnected effective coverage = %s %s %q", state, reason, next)
	}
}
