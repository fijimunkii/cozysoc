package storage

import (
	"errors"

	sqlite3 "modernc.org/sqlite/lib"
)

// IngestionHealthState describes current pipeline health. Historical counters are
// retained separately and do not keep the pipeline degraded after recovery.
type IngestionHealthState string

const (
	IngestionHealthCurrent      IngestionHealthState = "current"
	IngestionHealthPressure     IngestionHealthState = "pressure"
	IngestionHealthBackpressure IngestionHealthState = "backpressure"
	IngestionHealthWriteFailed  IngestionHealthState = "write-failed"
	IngestionHealthStorageFull  IngestionHealthState = "storage-full"
	IngestionHealthClosing      IngestionHealthState = "closing"
	IngestionHealthClosed       IngestionHealthState = "closed"
)

const (
	IngestionFailureWriteFailed = "write-failed"
	IngestionFailureSQLiteFull  = "sqlite-full"
)

type IngestionHealth struct {
	State        IngestionHealthState
	FailureClass string
	Capacity     int
	Depth        int
	Accepted     uint64
	Processed    uint64
	Dropped      uint64
	Failed       uint64
}

func (i *Ingestor) Health() IngestionHealth {
	if i == nil {
		return IngestionHealth{State: IngestionHealthClosed}
	}
	stats := i.Stats()

	i.episodeMu.Lock()
	overflowActive := i.overflowActive
	failureActive := i.failureActive
	failureClass := i.failureClass
	i.episodeMu.Unlock()

	health := IngestionHealth{
		State:        IngestionHealthCurrent,
		FailureClass: failureClass,
		Capacity:     stats.Capacity,
		Depth:        stats.Depth,
		Accepted:     stats.Accepted,
		Processed:    stats.Processed,
		Dropped:      stats.Dropped,
		Failed:       stats.Failed,
	}
	switch {
	case stats.Closed:
		health.State = IngestionHealthClosed
	case stats.Closing:
		health.State = IngestionHealthClosing
	case failureActive && failureClass == IngestionFailureSQLiteFull:
		health.State = IngestionHealthStorageFull
	case failureActive:
		health.State = IngestionHealthWriteFailed
	case overflowActive:
		health.State = IngestionHealthBackpressure
	case stats.Capacity > 0 && stats.Depth*4 >= stats.Capacity*3:
		health.State = IngestionHealthPressure
	}
	return health
}

type sqliteCodeError interface {
	error
	Code() int
}

func classifyIngestionFailure(err error) string {
	var coded sqliteCodeError
	if errors.As(err, &coded) && coded.Code()&0xff == sqlite3.SQLITE_FULL {
		return IngestionFailureSQLiteFull
	}
	return IngestionFailureWriteFailed
}
