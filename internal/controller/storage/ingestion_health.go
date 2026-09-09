package storage

// IngestionHealthState describes current pipeline health. Historical counters are
// retained separately and do not keep the pipeline degraded after recovery.
type IngestionHealthState string

const (
	IngestionHealthCurrent      IngestionHealthState = "current"
	IngestionHealthPressure     IngestionHealthState = "pressure"
	IngestionHealthBackpressure IngestionHealthState = "backpressure"
	IngestionHealthWriteFailed  IngestionHealthState = "write-failed"
	IngestionHealthClosing      IngestionHealthState = "closing"
	IngestionHealthClosed       IngestionHealthState = "closed"
)

type IngestionHealth struct {
	State     IngestionHealthState
	Capacity  int
	Depth     int
	Accepted  uint64
	Processed uint64
	Dropped   uint64
	Failed    uint64
}

func (i *Ingestor) Health() IngestionHealth {
	if i == nil {
		return IngestionHealth{State: IngestionHealthClosed}
	}
	stats := i.Stats()

	i.episodeMu.Lock()
	overflowActive := i.overflowActive
	failureActive := i.failureActive
	i.episodeMu.Unlock()

	health := IngestionHealth{
		State:     IngestionHealthCurrent,
		Capacity:  stats.Capacity,
		Depth:     stats.Depth,
		Accepted:  stats.Accepted,
		Processed: stats.Processed,
		Dropped:   stats.Dropped,
		Failed:    stats.Failed,
	}
	switch {
	case stats.Closed:
		health.State = IngestionHealthClosed
	case stats.Closing:
		health.State = IngestionHealthClosing
	case failureActive:
		health.State = IngestionHealthWriteFailed
	case overflowActive:
		health.State = IngestionHealthBackpressure
	case stats.Capacity > 0 && stats.Depth*4 >= stats.Capacity*3:
		health.State = IngestionHealthPressure
	}
	return health
}
