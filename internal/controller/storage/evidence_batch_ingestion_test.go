package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func unusedLegacyRepair(context.Context, *sql.Tx, Limits, domain.Observation) (bool, error) {
	return false, errors.New("unexpected legacy repair")
}

func TestBatchIngestorPrivateConnectionPolicyAndClose(t *testing.T) {
	s, _ := batchSQLFixture(t)
	r := batchSQLRecord(1)
	i, err := NewEvidenceBatchIngestor(s, 1, nil, func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return fixtureBatchPlan(r), nil
	}, unusedLegacyRepair)
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close(context.Background())
	owned := i.sink.(*evidenceBatchIngestionSink)
	if owned.conn == s.conn || owned.db == s.db {
		t.Fatal("shared writer")
	}
	for query, want := range map[string]int64{"PRAGMA foreign_keys": 1, "PRAGMA synchronous": 2, "PRAGMA trusted_schema": 0, "PRAGMA busy_timeout": 5000, "PRAGMA max_page_count": s.limits.MaxBytes / 4096} {
		var got int64
		if err := owned.conn.QueryRowContext(context.Background(), query).Scan(&got); err != nil || got != want {
			t.Fatal(query, got, err)
		}
	}
	if err := i.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := owned.conn.PingContext(context.Background()); !errors.Is(err, sql.ErrConnDone) {
		t.Fatal("owned connection remains open", err)
	}
	if err := s.conn.PingContext(context.Background()); err != nil {
		t.Fatal("parent closed", err)
	}
	if _, err := i.TrySubmitObservation(*r.Observation, nil); !errors.Is(err, ErrIngestorClosed) {
		t.Fatal(err)
	}
}

func TestBatchIngestorRequiresSchemaAndAdapters(t *testing.T) {
	s := openTestStore(t)
	planner := func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return EvidenceBatchPlan{}, nil
	}
	if i, err := NewEvidenceBatchIngestor(s, 1, nil, planner, nil); err == nil {
		i.Close(context.Background())
		t.Fatal("missing repair accepted")
	}
	if _, err := s.conn.ExecContext(context.Background(), "DROP TABLE evidence_batch_identity_routes"); err != nil {
		t.Fatal(err)
	}
	if i, err := NewEvidenceBatchIngestor(s, 1, nil, planner, unusedLegacyRepair); err == nil {
		i.Close(context.Background())
		t.Fatal("silently enabled incomplete schema")
	}
	if err := s.conn.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBatchIngestorBackpressureDrainAndFailure(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	planner := func(ctx context.Context, _ *MixedIdentitySnapshot, o domain.Observation) (EvidenceBatchPlan, error) {
		calls++
		if calls == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return EvidenceBatchPlan{}, ctx.Err()
			}
		}
		return EvidenceBatchPlan{}, errors.New("injected planner failure")
	}
	i, err := NewEvidenceBatchIngestor(s, 1, nil, planner, unusedLegacyRepair)
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close(context.Background())
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	first, err := i.SubmitObservation(context.Background(), *r.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("planner did not start")
	}
	second, err := i.TrySubmitObservation(*r.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i.TrySubmitObservation(*r.Observation, nil); !errors.Is(err, ErrQueueFull) {
		t.Fatal("queue overflow", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := first.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("receipt completed before persistence", err)
	}
	if err := i.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("close did not await drain", err)
	}
	close(release)
	for _, receipt := range []IngestionReceipt{first, second} {
		if got, err := receipt.Wait(context.Background()); err == nil || got.Inserted {
			t.Fatal("failed planner acknowledged", got, err)
		}
	}
	if err := i.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := i.Stats(); stats.Accepted != 2 || stats.Failed != 2 || stats.Processed != 0 || stats.Dropped != 1 || !stats.Closed {
		t.Fatal(stats)
	}
	assertStageTableCount(t, db, "evidence_batches", 0)
	assertStageTableCount(t, db, "devices", 0)
	if err := i.sink.(*evidenceBatchIngestionSink).conn.PingContext(context.Background()); !errors.Is(err, sql.ErrConnDone) {
		t.Fatal("timed-out Close leaked writer", err)
	}
}

func TestBatchIngestorCommitFailureDoesNotAcknowledge(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	i, err := NewEvidenceBatchIngestor(s, 1, nil, func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return fixtureBatchPlan(r), nil
	}, unusedLegacyRepair)
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close(context.Background())
	// A held reader allows writes to be staged but prevents rollback-journal commit.
	owned := i.sink.(*evidenceBatchIngestionSink)
	if _, err := owned.conn.ExecContext(context.Background(), "PRAGMA busy_timeout=20"); err != nil {
		t.Fatal(err)
	}
	reader, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var count int
	if err := reader.QueryRow("SELECT count(*) FROM devices").Scan(&count); err != nil {
		t.Fatal(err)
	}
	receipt, err := i.SubmitObservation(context.Background(), *r.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := receipt.Wait(context.Background()); err == nil || result.Inserted {
		t.Fatal("commit failure acknowledged", result, err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	// The failed receipt queues a storage event on the same writer. Restore
	// production timeouts before checking rollback and retrying, so its
	// concurrent audit write cannot make the fixture's 100 ms reader fail.
	if _, err := owned.conn.ExecContext(context.Background(), "PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	assertStageTableCount(t, db, "evidence_batches", 0)
	assertStageTableCount(t, db, "devices", 0)
	receipt, err = i.SubmitObservation(context.Background(), *r.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := receipt.Wait(context.Background()); err != nil || !result.Inserted {
		t.Fatal("retry failed", result, err)
	}
}

func TestBatchIngestorEnforcesQuotaAndRecovers(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	// Deterministic incompressible evidence forces allocation beyond existing pages.
	noise := make([]byte, 30000)
	_, _ = rand.New(rand.NewSource(1)).Read(noise)
	r.Observation.Payload = json.RawMessage(`{"noise":"` + hex.EncodeToString(noise) + `"}`)
	size, err := s.DatabaseBytes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.limits.MaxBytes = size
	i, err := NewEvidenceBatchIngestor(s, 1, nil, func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return fixtureBatchPlan(r), nil
	}, unusedLegacyRepair)
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close(context.Background())
	receipt, err := i.SubmitObservation(context.Background(), *r.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := receipt.Wait(context.Background()); err == nil || result.Inserted || classifyIngestionFailure(err) != IngestionFailureSQLiteFull {
		t.Fatal("quota bypassed", result, err)
	}
	if health := i.Health(); health.State != IngestionHealthStorageFull {
		t.Fatal(health)
	}
	assertStageTableCount(t, db, "evidence_batches", 0)
	assertStageTableCount(t, db, "devices", 0)
	// Test-only quota relief: the next successful complete write clears pressure.
	owned := i.sink.(*evidenceBatchIngestionSink)
	if _, err := owned.conn.ExecContext(context.Background(), "PRAGMA max_page_count=262144"); err != nil {
		t.Fatal(err)
	}
	receipt, err = i.SubmitObservation(context.Background(), *r.Observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := receipt.Wait(context.Background()); err != nil || !result.Inserted {
		t.Fatal("quota recovery failed", result, err)
	}
	if health := i.Health(); health.State != IngestionHealthCurrent {
		t.Fatal(health)
	}
}
