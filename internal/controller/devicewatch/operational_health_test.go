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
		state        storage.IngestionHealthState
		failureClass string
		want         OperationalState
		wantReason   string
	}{
		{state: storage.IngestionHealthCurrent, want: OperationalCurrent},
		{state: storage.IngestionHealthPressure, want: OperationalDegraded, wantReason: "queue-pressure"},
		{state: storage.IngestionHealthBackpressure, want: OperationalDegraded, wantReason: "backpressure"},
		{state: storage.IngestionHealthStorageFull, failureClass: storage.IngestionFailureSQLiteFull, want: OperationalDegraded, wantReason: "sqlite-full"},
		{state: storage.IngestionHealthWriteFailed, failureClass: storage.IngestionFailureWriteFailed, want: OperationalDegraded, wantReason: "write-failed"},
		{state: storage.IngestionHealthClosed, want: OperationalDisconnected, wantReason: "closed"},
	}
	for _, test := range tests {
		got := pipelineHealth(storage.IngestionHealth{State: test.state, FailureClass: test.failureClass, Capacity: 8, Depth: 2, Dropped: 3, Failed: 4})
		if got.State != test.want || got.Reason != test.wantReason || got.FailureClass != test.failureClass || got.Dropped != 3 || got.Failed != 4 {
			t.Fatalf("pipeline health for %s = %+v", test.state, got)
		}
	}
}

func TestDatabaseHealthDistinguishesQuotaAndFilesystemCapacity(t *testing.T) {
	tests := []struct {
		name   string
		health storage.Health
		want   OperationalState
		reason string
	}{
		{name: "current", health: storage.Health{State: storage.HealthCurrent, QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityCurrent, FilesystemSupported: true}, want: OperationalCurrent},
		{name: "quota pressure", health: storage.Health{State: storage.HealthPressure, QuotaState: storage.HealthPressure, FilesystemState: storage.FilesystemCapacityCurrent, FilesystemSupported: true}, want: OperationalDegraded, reason: "quota-pressure"},
		{name: "quota reached", health: storage.Health{State: storage.HealthAtQuota, QuotaState: storage.HealthAtQuota, FilesystemState: storage.FilesystemCapacityCurrent, FilesystemSupported: true}, want: OperationalDegraded, reason: "quota-reached"},
		{name: "filesystem pressure", health: storage.Health{State: storage.HealthFilesystemPressure, QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityPressure, FilesystemSupported: true}, want: OperationalDegraded, reason: "filesystem-pressure"},
		{name: "filesystem full", health: storage.Health{State: storage.HealthFilesystemFull, QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityFull, FilesystemSupported: true}, want: OperationalDegraded, reason: "filesystem-full"},
		{name: "filesystem unknown on supported platform", health: storage.Health{State: storage.HealthCurrent, QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityUnavailable, FilesystemSupported: true}, want: OperationalDegraded, reason: "filesystem-unknown"},
		{name: "filesystem unsupported", health: storage.Health{State: storage.HealthCurrent, QuotaState: storage.HealthCurrent, FilesystemState: storage.FilesystemCapacityUnavailable, FilesystemSupported: false}, want: OperationalCurrent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := databaseHealth(test.health)
			if got.State != test.want || got.Reason != test.reason {
				t.Fatalf("database health = %+v, want state=%s reason=%s", got, test.want, test.reason)
			}
		})
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

	health.Pipeline = PipelineHealth{State: OperationalDegraded, Reason: "sqlite-full", NextStep: "Wait for write recovery."}
	health.Database = DatabaseHealth{State: OperationalDegraded, Reason: "filesystem-full", NextStep: "Free disk space."}
	state, reason, next = EffectiveCoverage(report, health)
	if state != CoverageDegraded || reason != "storage-filesystem-full" || next != "Free disk space." {
		t.Fatalf("diagnosed filesystem full coverage = %s %s %q", state, reason, next)
	}

	health.Database = DatabaseHealth{State: OperationalCurrent}
	state, reason, next = EffectiveCoverage(report, health)
	if state != CoverageDegraded || reason != "ingestion-sqlite-full" || next != "Wait for write recovery." {
		t.Fatalf("awaiting sqlite full recovery = %s %s %q", state, reason, next)
	}

	health.Sensor = SensorHealth{State: OperationalDisconnected, NextStep: "Restart Device Watch."}
	state, reason, next = EffectiveCoverage(report, health)
	if state != CoverageDisconnected || reason != "sensor-disconnected" || next != "Restart Device Watch." {
		t.Fatalf("disconnected effective coverage = %s %s %q", state, reason, next)
	}
}
