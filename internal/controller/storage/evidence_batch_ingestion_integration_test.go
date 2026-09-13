package storage_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func newBatchQueue(t *testing.T, s *storage.Store) *storage.Ingestor {
	t.Helper()
	i, err := storage.NewEvidenceBatchIngestor(s, 4, nil, devicewatch.PlanBatchEvidence, devicewatch.RepairLegacyObservation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := i.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return i
}
func queuedObservation(t *testing.T, i *storage.Ingestor, o domain.Observation, c *domain.IngestionCheckpoint) (storage.IngestionResult, error) {
	t.Helper()
	receipt, err := i.SubmitObservation(context.Background(), o, c)
	if err != nil {
		return storage.IngestionResult{}, err
	}
	return receipt.Wait(context.Background())
}
func newQueuedObservation(o domain.Observation) domain.Observation {
	o.ID = "obs.new.batch"
	o.SourceKey = "source.new.batch"
	return o
}

func TestBatchQueueRealReconciliationReplayAndCheckpointAtomicity(t *testing.T) {
	s, db, legacy := legacyRepairFixture(t)
	i := newBatchQueue(t, s)
	o := newQueuedObservation(legacy)
	cp := domain.IngestionCheckpoint{SensorID: o.SensorID, StreamID: o.SourceStream, Cursor: "first", UpdatedAt: time.Now().UTC()}
	// The checkpoint is deliberately the last write after device/batch/index work.
	if _, err := db.Exec(`CREATE TRIGGER reject_batch_checkpoint BEFORE INSERT ON ingestion_checkpoints BEGIN SELECT RAISE(ABORT,'injected checkpoint failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if result, err := queuedObservation(t, i, o, &cp); err == nil || result.Inserted {
		t.Fatal("failed transaction acknowledged", result, err)
	}
	for _, table := range []string{"evidence_batches", "evidence_batch_lookup", "devices", "ingestion_checkpoints"} {
		legacyRowCount(t, db, table, 0)
	}
	if _, err := db.Exec("DROP TRIGGER reject_batch_checkpoint"); err != nil {
		t.Fatal(err)
	}
	if result, err := queuedObservation(t, i, o, &cp); err != nil || !result.Inserted {
		t.Fatal(result, err)
	}
	var raw []byte
	if err := db.QueryRow("SELECT data FROM evidence_batches").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	records, err := storage.DecodeEvidenceBatch(raw)
	if err != nil || len(records) != 1 || len(records[0].Claims) != 2 || len(records[0].Links) != 2 {
		t.Fatal(records, err)
	}
	if records[0].Observation.ID != o.ID || string(records[0].Observation.Payload) != string(o.Payload) {
		t.Fatal("original changed")
	}
	cp.Cursor = "replayed"
	retry := o
	retry.ID = "obs.changed.retry"
	retry.Kind = "other-observation-kind"
	retry.Payload = json.RawMessage(`{"changed":true}`)
	if result, err := queuedObservation(t, i, retry, &cp); err != nil || result.Inserted {
		t.Fatal("batch replay replanned", result, err)
	}
	var after []byte
	if err := db.QueryRow("SELECT data FROM evidence_batches").Scan(&after); err != nil || string(after) != string(raw) {
		t.Fatal("replay changed stored evidence", err)
	}
	got, ok, err := s.LoadCheckpoint(context.Background(), o.SensorID, o.SourceStream)
	if err != nil || !ok || got.Cursor != "replayed" {
		t.Fatal(got, ok, err)
	}
	legacyRowCount(t, db, "observations", 1)
	legacyRowCount(t, db, "evidence_batch_lookup", 1)
	collision := o
	collision.Kind = "other-observation-kind"
	collision.SourceKey = "different.source.key"
	if result, err := queuedObservation(t, i, collision, nil); err == nil || result.Inserted {
		t.Fatal("other kind bypassed batch ID conflict", result, err)
	}
	if err := i.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A fresh connection verifies that the receipt described durable rows.
	uri := url.URL{Scheme: "file", Path: s.Path(), RawQuery: "mode=ro"}
	reopened, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	legacyRowCount(t, reopened, "evidence_batches", 1)
	legacyRowCount(t, reopened, "devices", 1)
}

func TestBatchQueueLegacyDispatchAndOtherObservationFallback(t *testing.T) {
	s, db, original := legacyRepairFixture(t)
	i := newBatchQueue(t, s)
	retry := original
	retry.ID = "obs.retry"
	retry.Kind = "other-observation-kind"
	retry.Payload = json.RawMessage(`{"changed":true}`)
	if result, err := queuedObservation(t, i, retry, nil); err != nil || result.Inserted {
		t.Fatal("legacy repair failed", result, err)
	}
	legacyRowCount(t, db, "observations", 1)
	legacyRowCount(t, db, "identity_claims", 2)
	legacyRowCount(t, db, "device_claim_links", 2)
	legacyRowCount(t, db, "evidence_batches", 0)
	other := original
	other.ID = "obs.other"
	other.SourceKey = "other"
	other.Kind = "fixture-observation"
	cp := domain.IngestionCheckpoint{SensorID: other.SensorID, StreamID: other.SourceStream, Cursor: "other", UpdatedAt: time.Now().UTC()}
	if result, err := queuedObservation(t, i, other, &cp); err != nil || !result.Inserted {
		t.Fatal("other observation failed", result, err)
	}
	legacyRowCount(t, db, "observations", 2)
	got, ok, err := s.LoadCheckpoint(context.Background(), other.SensorID, other.SourceStream)
	if err != nil || !ok || got.Cursor != "other" {
		t.Fatal(got, ok, err)
	}
	if result, err := queuedObservation(t, i, other, &cp); err != nil || result.Inserted {
		t.Fatal("other observation replay failed", result, err)
	}
	other.Kind = "device-neighbor-seen"
	if result, err := queuedObservation(t, i, other, &cp); err != nil || result.Inserted {
		t.Fatal("changed retry kind replaced legacy evidence", result, err)
	}
	// Expired legacy evidence remains deduplicated and cannot be repaired anew.
	if _, err := db.Exec("DELETE FROM device_claim_links; DELETE FROM identity_claims; UPDATE observations SET expires_at_ns=1 WHERE id=?", original.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := queuedObservation(t, i, retry, nil); err != nil || result.Inserted {
		t.Fatal(result, err)
	}
	legacyRowCount(t, db, "identity_claims", 0)
}

func TestBatchQueueCollectorReconcilesBeforeReceipt(t *testing.T) {
	s, db, o := legacyRepairFixture(t)
	i := newBatchQueue(t, s)
	source := &batchWorkloadSource{snapshot: devicewatch.Snapshot{CapturedAt: time.Now().UTC().Add(-time.Hour), InterfaceName: "fixture0", Sources: []devicewatch.SourceStatus{{Method: devicewatch.MethodARPCache, Available: true}, {Method: devicewatch.MethodNDPCache, Available: true}}}}
	for n := 0; n < 100; n++ {
		mac, err := net.ParseMAC(fmt.Sprintf("02:00:00:00:00:%02x", n+1))
		if err != nil {
			t.Fatal(err)
		}
		source.snapshot.Neighbors = append(source.snapshot.Neighbors, devicewatch.Neighbor{Address: netip.MustParseAddr(fmt.Sprintf("192.168.50.%d", n+10)), HardwareAddr: mac, InterfaceName: "fixture0", Method: devicewatch.MethodARPCache})
	}
	// No ReconcilingSink: a successful queue receipt includes the entire decision.
	collector, err := devicewatch.NewCollector(source, batchWorkloadInspector{}, devicewatch.StorageSink{Ingestor: i})
	if err != nil {
		t.Fatal(err)
	}
	binding := devicewatch.ScopeBinding{InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}
	for round := 0; round < 4; round++ {
		result, err := collector.CollectOnce(context.Background(), o.ScopeID, o.SensorID, binding)
		if err != nil || result.Visible != 100 || result.Inserted != 100 || result.Deduplicated != 0 {
			t.Fatal(result, err)
		}
		source.snapshot.CapturedAt = source.snapshot.CapturedAt.Add(time.Minute)
		legacyRowCount(t, db, "devices", 100)
		legacyRowCount(t, db, "evidence_batch_lookup", (round+1)*100)
		legacyRowCount(t, db, "coverage_samples", round+1)
	}
	if err := i.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT data FROM evidence_batches")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	observations, claims, links := 0, 0, 0
	deviceIDs := map[string]bool{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		records, err := storage.DecodeEvidenceBatch(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Observation == nil || record.Observation.SourceTime == nil || record.Observation.ScopeID != o.ScopeID {
				t.Fatal("missing original evidence")
			}
			observations++
			claims += len(record.Claims)
			links += len(record.Links)
			for _, link := range record.Links {
				deviceIDs[link.DeviceID] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if observations != 400 || claims != 800 || links != 800 {
		t.Fatal(observations, claims, links)
	}
	if stats := i.Stats(); stats.Accepted != 404 || stats.Processed != 404 || stats.Failed != 0 || stats.Dropped != 0 {
		t.Fatal(stats)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := storage.NewMixedIdentitySnapshot(tx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for id := range deviceIDs {
		detail, err := reader.GetDeviceEvidenceDetail(context.Background(), storage.DeviceEvidenceDetailQuery{ScopeID: o.ScopeID, DeviceID: id, AsOf: source.snapshot.CapturedAt})
		if err != nil || len(detail.Evidence) != 8 || detail.Truncated || !detail.Summary.LastSeen.Equal(source.snapshot.CapturedAt.Add(-time.Minute)) {
			t.Fatal(detail, err)
		}
		observations := map[string]bool{}
		for _, item := range detail.Evidence {
			if item.Observation == nil {
				t.Fatal("missing retained source")
			}
			observations[item.Observation.ID] = true
		}
		if len(observations) != 4 {
			t.Fatal("missing collected observations", len(observations))
		}
	}

}
