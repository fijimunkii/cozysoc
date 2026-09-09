package main

import (
	"time"

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
	return &api.DeviceWatchOperationalHealth{
		Sensor: sensor,
		Pipeline: api.DeviceWatchPipelineHealth{
			State:    string(health.Pipeline.State),
			Reason:   health.Pipeline.Reason,
			Capacity: health.Pipeline.Capacity,
			Depth:    health.Pipeline.Depth,
			Dropped:  health.Pipeline.Dropped,
			Failed:   health.Pipeline.Failed,
			NextStep: health.Pipeline.NextStep,
		},
		Database: api.DeviceWatchDatabaseHealth{
			State:         string(health.Database.State),
			Reason:        health.Database.Reason,
			DatabaseBytes: health.Database.DatabaseBytes,
			MaxBytes:      health.Database.MaxBytes,
			NextStep:      health.Database.NextStep,
		},
	}
}

func copyTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copyValue := value.UTC()
	return &copyValue
}
