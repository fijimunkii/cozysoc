package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// EvidenceBatchLegacyRepair is a trusted producer adapter, never supplied by a
// network or UI request. It must use the provided transaction without committing.
type EvidenceBatchLegacyRepair func(context.Context, *sql.Tx, Limits, domain.Observation) (bool, error)

// NewEvidenceBatchIngestor connects the bounded ingestion queue to atomic Device
// Watch evidence writes. The reserved schema must already exist. This constructor
// does not migrate or enable live storage; reader, lifecycle and compatibility
// gates must be completed before runtime wiring. Do not wrap this queue in a
// second reconciler: its successful receipts already include reconciliation.
// Close the ingestor before its parent Store; Close drains accepted work and
// releases the private writer even if an earlier Close call timed out.
func NewEvidenceBatchIngestor(store *Store, capacity int, logger *slog.Logger, planner EvidenceBatchPlanner, repair EvidenceBatchLegacyRepair) (*Ingestor, error) {
	if store == nil || store.conn == nil || repair == nil {
		return nil, fmt.Errorf("batch ingestion requires storage and legacy repair")
	}
	stager, err := NewEvidenceBatchStager(store.limits, planner)
	if err != nil {
		return nil, err
	}
	limits, err := normalizeLimits(store.limits)
	if err != nil {
		return nil, err
	}
	dsn, err := sqliteFileURI(store.path)
	if err != nil {
		return nil, err
	}
	// A separate pinned connection prevents any Store autocommit operation or
	// history reader from joining the queue's transaction. mode=rw cannot create
	// a replacement database if the original path disappears.
	db, err := sql.Open("sqlite", dsn+"?mode=rw")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	writer := &Store{db: db, conn: conn, path: store.path, limits: limits, now: time.Now}
	fail := func(err error) (*Ingestor, error) { return nil, errors.Join(err, writer.Close()) }
	if err := writer.configure(ctx); err != nil {
		return fail(err)
	}
	// max_page_count is connection-local. A fresh writer must not bypass the
	// parent's quota, even though both connections write the same database file.
	if err := writer.applyQuota(ctx); err != nil {
		return fail(err)
	}
	rows, err := conn.QueryContext(ctx, `SELECT b.data,l.slot,s.scope_id,g.routing,r.kind FROM evidence_batches b
	 JOIN evidence_batch_lookup l ON l.batch_id=b.id JOIN evidence_batch_sources s ON s.id=b.source_id
	 JOIN evidence_batch_identity_groups g ON g.id=b.identity_group
	 JOIN evidence_batch_identity_routes r ON r.group_id=g.id LIMIT 0`)
	if err != nil {
		return fail(fmt.Errorf("batch ingestion requires reserved schema: %w", err))
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	sink := &evidenceBatchIngestionSink{Store: writer, stager: stager, repair: repair}
	i, err := newIngestor(sink, capacity, logger)
	if err != nil {
		return fail(err)
	}
	return i, nil
}

type evidenceBatchIngestionSink struct {
	*Store
	stager *EvidenceBatchStager
	repair EvidenceBatchLegacyRepair
}

func (s *evidenceBatchIngestionSink) closeIngestionSink() error { return s.Store.Close() }

func (s *evidenceBatchIngestionSink) ingestObservation(ctx context.Context, o domain.Observation, checkpoint *domain.IngestionCheckpoint) (bool, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	inserted, err := s.stageObservation(ctx, tx, o)
	if errors.Is(err, ErrEvidenceBatchLegacyReplay) {
		// Preserve the false insertion result even when a partial legacy record
		// is repaired. Expired originals also remain deduplicated, not revived.
		var neighbor bool
		err = tx.QueryRowContext(ctx, `SELECT kind='device-neighbor-seen' FROM observations
		 WHERE sensor_id=? AND source_stream=? AND source_key=?`, o.SensorID, o.SourceStream, o.SourceKey).Scan(&neighbor)
		if err == nil && neighbor {
			_, err = s.repair(ctx, tx, s.limits, o)
		}
	}
	if err != nil {
		return false, err
	}
	if checkpoint != nil {
		if err := saveCheckpoint(ctx, tx, *checkpoint); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, wrapWrite("commit batch ingestion", err)
	}
	return inserted, nil
}

func (s *evidenceBatchIngestionSink) stageObservation(ctx context.Context, tx *sql.Tx, o domain.Observation) (bool, error) {
	if o.Kind == "device-neighbor-seen" {
		_, inserted, err := s.stager.Stage(ctx, tx, o)
		return inserted, err
	}
	// Other observation kinds stay in legacy SQL, but cannot bypass a retained
	// batch's source key or ID by changing the kind on a retry. Keep this check
	// and any new legacy insertion in the same transaction as its checkpoint.
	replay, err := evidenceBatchObservationReplay(ctx, tx, o)
	if err != nil || replay {
		return false, err
	}
	return insertObservation(ctx, tx, o, s.expiry)
}
