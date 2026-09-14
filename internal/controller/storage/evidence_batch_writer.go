package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// openEvidenceBatchWriter owns a separate pinned connection. Its caller must
// serialize use, finish every transaction and close it before the parent store.
// Opening never installs the reserved schema or creates a missing database.
func openEvidenceBatchWriter(ctx context.Context, store *Store) (*Store, error) {
	if store == nil || store.conn == nil {
		return nil, fmt.Errorf("batch writer requires storage")
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
	// history reader from joining its transaction. mode=rw cannot create
	// a replacement database if the original path disappears.
	db, err := sql.Open("sqlite", dsn+"?mode=rw")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	writer := &Store{db: db, conn: conn, path: store.path, limits: limits, now: time.Now}
	fail := func(err error) (*Store, error) { return nil, errors.Join(err, writer.Close()) }
	if err := writer.configure(ctx); err != nil {
		return fail(err)
	}
	// max_page_count is connection-local. A fresh writer must not bypass the
	// parent's quota, even though both connections write the same database file.
	if err := writer.applyQuota(ctx); err != nil {
		return fail(err)
	}
	rows, err := conn.QueryContext(ctx, `SELECT b.data,b.entries,b.next_expiry_ns,b.first_claim_ns,b.last_claim_ns,b.last_claim_expiry_ns,
 b.first_observation_ns,b.last_observation_ns,b.last_observation_expiry_ns,
 l.slot,s.scope_id,g.routing,r.kind FROM evidence_batches b
	 JOIN evidence_batch_lookup l ON l.batch_id=b.id JOIN evidence_batch_sources s ON s.id=b.source_id
	 JOIN evidence_batch_identity_groups g ON g.id=b.identity_group
	 JOIN evidence_batch_identity_routes r ON r.group_id=g.id LIMIT 0`)
	if err != nil {
		return fail(fmt.Errorf("batch writer requires reserved schema: %w", err))
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	return writer, nil
}
