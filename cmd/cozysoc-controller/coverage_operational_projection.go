package main

import (
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
)

func projectDeviceWatchOperational(health devicewatch.OperationalHealth) *api.DeviceWatchOperationalHealth {
	sensor := api.DeviceWatchSensorHealth{
		State:          string(health.Sensor.State),
		Running:        health.Sensor.Running,
		LastErrorClass: health.Sensor.LastErrorClass,
		NextStep:       health.Sensor.NextStep,
	}
	if !health.Sensor.LastAttemptAt.IsZero() {
		value := health.Sensor.LastAttemptAt.UTC()
		sensor.LastAttemptAt = &value
	}
	if !health.Sensor.LastSuccessfulAt.IsZero() {
		value := health.Sensor.LastSuccessfulAt.UTC()
		sensor.LastSuccessfulAt = &value
	}
	pipeline := api.DeviceWatchPipelineHealth{
		State:                string(health.Pipeline.State),
		Reason:               health.Pipeline.Reason,
		FailureClass:         health.Pipeline.FailureClass,
		Capacity:             health.Pipeline.Capacity,
		Depth:                health.Pipeline.Depth,
		QueuePressure:        health.Pipeline.QueuePressure,
		Dropped:              health.Pipeline.Dropped,
		Failed:               health.Pipeline.Failed,
		LatencyState:         string(health.Pipeline.LatencyState),
		LatencyThresholdMS:   health.Pipeline.LatencyThreshold.Milliseconds(),
		Pending:              health.Pipeline.Pending,
		OldestPendingMS:      health.Pipeline.OldestPendingAge.Milliseconds(),
		LastDurableLatencyMS: health.Pipeline.LastDurableLatency.Milliseconds(),
		LastQueueWaitMS:      health.Pipeline.LastQueueWait.Milliseconds(),
		LastProcessingMS:     health.Pipeline.LastProcessingDuration.Milliseconds(),
		SlowStreak:           health.Pipeline.SlowStreak,
		NextStep:             health.Pipeline.NextStep,
	}
	if !health.Pipeline.LastCompletedAt.IsZero() {
		value := health.Pipeline.LastCompletedAt.UTC()
		pipeline.LastCompletedAt = &value
	}
	return &api.DeviceWatchOperationalHealth{
		Sensor:   sensor,
		Pipeline: pipeline,
		Database: api.DeviceWatchDatabaseHealth{
			State:                     string(health.Database.State),
			Reason:                    health.Database.Reason,
			QuotaState:                string(health.Database.QuotaState),
			FilesystemState:           string(health.Database.FilesystemState),
			FilesystemSupported:       health.Database.FilesystemSupported,
			DatabaseBytes:             health.Database.DatabaseBytes,
			UsedBytes:                 health.Database.UsedBytes,
			ReusableBytes:             health.Database.ReusableBytes,
			MaxBytes:                  health.Database.MaxBytes,
			FilesystemTotalBytes:      health.Database.FilesystemTotalBytes,
			FilesystemAvailableBytes:  health.Database.FilesystemAvailableBytes,
			FilesystemPressureAtBytes: health.Database.FilesystemPressureAtBytes,
			NextStep:                  health.Database.NextStep,
		},
	}
}
