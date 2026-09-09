package devicewatch

import (
	"context"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type OperationalState string

const (
	OperationalCurrent      OperationalState = "current"
	OperationalStarting     OperationalState = "starting"
	OperationalDegraded     OperationalState = "degraded"
	OperationalStale        OperationalState = "stale"
	OperationalDisconnected OperationalState = "disconnected"
	OperationalUnavailable  OperationalState = "unavailable"
)

type SensorHealth struct {
	State            OperationalState
	Running          bool
	LastAttemptAt    time.Time
	LastSuccessfulAt time.Time
	LastErrorClass   string
	NextStep         string
}

type PipelineHealth struct {
	State                  OperationalState
	Reason                 string
	FailureClass           string
	Capacity               int
	Depth                  int
	QueuePressure          bool
	Dropped                uint64
	Failed                 uint64
	LatencyState           storage.IngestionLatencyState
	LatencyThreshold       time.Duration
	Pending                int
	OldestPendingAge       time.Duration
	LastDurableLatency     time.Duration
	LastQueueWait          time.Duration
	LastProcessingDuration time.Duration
	LastCompletedAt        time.Time
	SlowStreak             int
	NextStep               string
}

type DatabaseHealth struct {
	State                     OperationalState
	Reason                    string
	QuotaState                storage.HealthState
	FilesystemState           storage.FilesystemCapacityState
	FilesystemSupported       bool
	DatabaseBytes             int64
	UsedBytes                 int64
	ReusableBytes             int64
	MaxBytes                  int64
	FilesystemTotalBytes      int64
	FilesystemAvailableBytes  int64
	FilesystemPressureAtBytes int64
	NextStep                  string
}

type OperationalHealth struct {
	Sensor   SensorHealth
	Pipeline PipelineHealth
	Database DatabaseHealth
}

type operationalRuntime interface {
	State() RuntimeState
	IngestionHealth() storage.IngestionHealth
}

func (r *Runtime) IngestionHealth() storage.IngestionHealth {
	if r == nil || r.ingestor == nil {
		return storage.IngestionHealth{State: storage.IngestionHealthClosed}
	}
	return r.ingestor.Health()
}

func (d *LifecycleDriver) OperationalHealth(ctx context.Context, now time.Time) (OperationalHealth, error) {
	now = now.UTC()
	operational := OperationalHealth{
		Sensor:   sensorHealth(nil, now),
		Pipeline: pipelineHealth(storage.IngestionHealth{State: storage.IngestionHealthClosed}),
	}
	if d != nil && d.runtime != nil {
		operational.Sensor = sensorHealth(d.runtime, now)
		operational.Pipeline = pipelineHealth(d.runtime.IngestionHealth())
	}
	if d != nil && d.store != nil {
		health, err := d.store.Health(ctx)
		if err != nil {
			return OperationalHealth{}, err
		}
		operational.Database = databaseHealth(health)
	} else {
		operational.Database = DatabaseHealth{
			State:    OperationalUnavailable,
			Reason:   "storage-unavailable",
			NextStep: "Restart the controller before relying on Device Watch storage health.",
		}
	}
	return operational, nil
}

func sensorHealth(runtime operationalRuntime, now time.Time) SensorHealth {
	if runtime == nil {
		return SensorHealth{
			State:    OperationalUnavailable,
			NextStep: "Use Device Watch only on a platform with a supported passive runtime.",
		}
	}
	state := runtime.State()
	health := SensorHealth{
		Running:          state.Running,
		LastAttemptAt:    state.LastAttemptAt.UTC(),
		LastSuccessfulAt: state.LastSuccessfulAt.UTC(),
		LastErrorClass:   state.LastErrorClass,
	}
	switch {
	case !state.Running:
		health.State = OperationalDisconnected
		health.NextStep = "Restart Device Watch and confirm the enrolled network is still available."
	case state.LastErrorClass != "":
		health.State = OperationalDegraded
		health.NextStep = sensorErrorNextStep(state.LastErrorClass)
	case state.LastAttemptAt.IsZero() || state.LastSuccessfulAt.IsZero():
		health.State = OperationalStarting
		health.NextStep = "Wait for the first Device Watch collection to complete."
	case state.LastSuccessfulAt.After(now.Add(coverageFutureTolerance)):
		health.State = OperationalDegraded
		health.NextStep = "Correct the controller clock before relying on sensor timestamps."
	case now.Sub(state.LastSuccessfulAt) > coverageFreshnessWindow:
		health.State = OperationalStale
		health.NextStep = "Wake or reconnect the enrolled Mac and confirm Device Watch is still running."
	default:
		health.State = OperationalCurrent
	}
	return health
}

func sensorErrorNextStep(reason string) string {
	switch reason {
	case "scope-mismatch", "unsupported-interface":
		return "Return to the enrolled network or re-enroll the intended local interface before trusting Device Watch coverage."
	case "source-unavailable":
		return "Check local ARP/NDP neighbor-table access on the enrolled Mac."
	case "source-oversized":
		return "Inspect local neighbor-table behavior before increasing any collection limits."
	case "canceled":
		return "Confirm the controller is awake and Device Watch is still enabled."
	default:
		return "Inspect local Device Watch diagnostics and restore successful collection before trusting current coverage."
	}
}

func pipelineHealth(health storage.IngestionHealth) PipelineHealth {
	result := PipelineHealth{
		State:                  OperationalCurrent,
		FailureClass:           health.FailureClass,
		Capacity:               health.Capacity,
		Depth:                  health.Depth,
		QueuePressure:          health.QueuePressure,
		Dropped:                health.Dropped,
		Failed:                 health.Failed,
		LatencyState:           health.LatencyState,
		LatencyThreshold:       health.LatencyThreshold,
		Pending:                health.Pending,
		OldestPendingAge:       health.OldestPendingAge,
		LastDurableLatency:     health.LastDurableLatency,
		LastQueueWait:          health.LastQueueWait,
		LastProcessingDuration: health.LastProcessingDuration,
		LastCompletedAt:        health.LastCompletedAt,
		SlowStreak:             health.SlowStreak,
	}
	switch health.State {
	case storage.IngestionHealthCurrent:
		if health.QueuePressure {
			result.Reason = "queue-pressure"
			result.NextStep = "Queue utilization is high, but measured accepted-to-durable latency remains within the current lag threshold."
		}
		return result
	case storage.IngestionHealthLagging:
		result.State = OperationalDegraded
		result.Reason = "latency"
		result.NextStep = "Restore ingestion throughput; accepted evidence is taking too long to reach durable storage."
	case storage.IngestionHealthBackpressure:
		result.State = OperationalDegraded
		result.Reason = "backpressure"
		result.NextStep = "Restore ingestion throughput; some evidence has been dropped while the queue was saturated."
	case storage.IngestionHealthStorageFull:
		result.State = OperationalDegraded
		result.Reason = "sqlite-full"
		result.NextStep = "SQLite rejected a write as full. Free the limiting database or host-volume capacity shown below, then wait for a successful collection to prove write recovery."
	case storage.IngestionHealthWriteFailed:
		result.State = OperationalDegraded
		result.Reason = "write-failed"
		result.NextStep = "Restore controller storage writes before trusting newly collected evidence."
	case storage.IngestionHealthClosing:
		result.State = OperationalDegraded
		result.Reason = "closing"
		result.NextStep = "Allow controller shutdown to complete before evaluating coverage."
	case storage.IngestionHealthClosed:
		result.State = OperationalDisconnected
		result.Reason = "closed"
		result.NextStep = "Restart the controller before relying on new Device Watch evidence."
	default:
		result.State = OperationalDegraded
		result.Reason = "unknown"
		result.NextStep = "Inspect ingestion diagnostics before trusting new Device Watch evidence."
	}
	return result
}

func databaseHealth(health storage.Health) DatabaseHealth {
	result := DatabaseHealth{
		State:                     OperationalCurrent,
		QuotaState:                health.QuotaState,
		FilesystemState:           health.FilesystemState,
		FilesystemSupported:       health.FilesystemSupported,
		DatabaseBytes:             health.DatabaseBytes,
		UsedBytes:                 health.UsedBytes,
		ReusableBytes:             health.ReusableBytes,
		MaxBytes:                  health.MaxBytes,
		FilesystemTotalBytes:      health.FilesystemTotalBytes,
		FilesystemAvailableBytes:  health.FilesystemAvailableBytes,
		FilesystemPressureAtBytes: health.FilesystemPressureAtBytes,
	}
	switch health.State {
	case storage.HealthCurrent:
		if health.FilesystemSupported && health.FilesystemState == storage.FilesystemCapacityUnavailable {
			result.State = OperationalDegraded
			result.Reason = "filesystem-unknown"
			result.NextStep = "Check the controller state-directory volume before relying on filesystem-capacity health."
		}
		return result
	case storage.HealthPressure:
		result.State = OperationalDegraded
		result.Reason = "quota-pressure"
		result.NextStep = "Review retention and storage growth before the Cozy SOC database reaches its configured quota."
	case storage.HealthAtQuota:
		result.State = OperationalDegraded
		result.Reason = "quota-reached"
		result.NextStep = "Free Cozy SOC database capacity before relying on new evidence writes."
	case storage.HealthFilesystemPressure:
		result.State = OperationalDegraded
		result.Reason = "filesystem-pressure"
		result.NextStep = "Free space on the volume containing the Cozy SOC state directory before available write headroom is exhausted."
	case storage.HealthFilesystemFull:
		result.State = OperationalDegraded
		result.Reason = "filesystem-full"
		result.NextStep = "Free space on the volume containing the Cozy SOC state directory before relying on new evidence writes."
	default:
		result.State = OperationalDegraded
		result.Reason = "unknown"
		result.NextStep = "Inspect controller storage health before relying on new evidence writes."
	}
	return result
}

func sensorVerificationSignal(health SensorHealth) capability.VerificationSignal {
	signal := capability.VerificationSignal{ID: "sensor-operational"}
	switch health.State {
	case OperationalCurrent:
		signal.Status = capability.SignalFresh
	case OperationalStarting:
		signal.Status = capability.SignalMissing
		signal.Message = "Device Watch has not completed its first collection"
	case OperationalStale:
		signal.Status = capability.SignalStale
		signal.Message = "Device Watch has not completed a successful collection within the freshness window"
	default:
		signal.Status = capability.SignalFailed
		signal.Message = "Device Watch sensor runtime is not currently healthy"
	}
	return signal
}

func pipelineVerificationSignal(health PipelineHealth) capability.VerificationSignal {
	signal := capability.VerificationSignal{ID: "ingestion-health"}
	if health.State == OperationalCurrent {
		signal.Status = capability.SignalFresh
		return signal
	}
	signal.Status = capability.SignalFailed
	signal.Message = "Device Watch ingestion pipeline is degraded"
	return signal
}

func databaseVerificationSignal(health DatabaseHealth) capability.VerificationSignal {
	signal := capability.VerificationSignal{ID: "storage-health"}
	if health.State == OperationalCurrent {
		signal.Status = capability.SignalFresh
		return signal
	}
	signal.Status = capability.SignalFailed
	signal.Message = "Device Watch storage capacity is degraded"
	return signal
}
