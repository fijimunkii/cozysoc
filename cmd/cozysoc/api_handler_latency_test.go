package main

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestControllerAPIHandlerKeepsQueuePressureDiagnosticWhenLatencyIsCurrent(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	storeFixture := coverageControllerStore(t, now, true, true)
	operational := healthyOperational(now)
	operational.Pipeline = devicewatch.PipelineHealth{
		State:                  devicewatch.OperationalCurrent,
		Reason:                 "queue-pressure",
		Capacity:               4,
		Depth:                  3,
		QueuePressure:          true,
		LatencyState:           storage.IngestionLatencyCurrent,
		LatencyThreshold:       5 * time.Second,
		LastDurableLatency:     300 * time.Millisecond,
		LastQueueWait:          100 * time.Millisecond,
		LastProcessingDuration: 200 * time.Millisecond,
		LastCompletedAt:        now.Add(-time.Second),
		NextStep:               "Measured latency remains current.",
	}
	control := &fakeOperationalCoverageControl{
		fakeDeviceWatchAPIControl: &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true},
		operational:               operational,
	}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), storeFixture, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	result, err := handler.DeviceWatchCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "active-limited" || result.Reason != "fresh-limited" {
		t.Fatalf("queue pressure alone degraded coverage: %+v", result)
	}
	pipeline := result.Operational.Pipeline
	if pipeline.State != "current" || pipeline.Reason != "queue-pressure" || !pipeline.QueuePressure || pipeline.LatencyState != "current" {
		t.Fatalf("queue pressure diagnostics = %+v", pipeline)
	}
	if pipeline.LatencyThresholdMS != 5000 || pipeline.LastDurableLatencyMS != 300 || pipeline.LastQueueWaitMS != 100 || pipeline.LastProcessingMS != 200 || pipeline.LastCompletedAt == nil || !pipeline.LastCompletedAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("latency projection = %+v", pipeline)
	}
}

func TestControllerAPIHandlerDegradesOnMeasuredIngestionLag(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	storeFixture := coverageControllerStore(t, now, true, true)
	operational := healthyOperational(now)
	operational.Pipeline = devicewatch.PipelineHealth{
		State:                  devicewatch.OperationalDegraded,
		Reason:                 "latency",
		Capacity:               256,
		Depth:                  1,
		LatencyState:           storage.IngestionLatencyLagging,
		LatencyThreshold:       5 * time.Second,
		Pending:                1,
		OldestPendingAge:       7 * time.Second,
		LastDurableLatency:     6 * time.Second,
		LastQueueWait:          5 * time.Second,
		LastProcessingDuration: time.Second,
		LastCompletedAt:        now.Add(-2 * time.Second),
		SlowStreak:             3,
		NextStep:               "Restore ingestion throughput.",
	}
	control := &fakeOperationalCoverageControl{
		fakeDeviceWatchAPIControl: &fakeDeviceWatchAPIControl{scopeID: "scope.home", configured: true},
		operational:               operational,
	}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), storeFixture, control)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	result, err := handler.DeviceWatchCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "degraded" || result.Reason != "ingestion-latency" || result.NextStep != "Restore ingestion throughput." {
		t.Fatalf("measured ingestion lag coverage = %+v", result)
	}
	pipeline := result.Operational.Pipeline
	if pipeline.State != "degraded" || pipeline.LatencyState != "lagging" || pipeline.Pending != 1 || pipeline.OldestPendingMS != 7000 || pipeline.SlowStreak != 3 {
		t.Fatalf("lagging pipeline projection = %+v", pipeline)
	}
}
