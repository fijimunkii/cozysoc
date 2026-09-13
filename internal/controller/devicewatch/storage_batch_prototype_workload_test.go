package devicewatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// This measures only the standalone prototype. Production identity queries,
// device/coverage storage, retention pruning, quota and history APIs are absent.
func TestPrototypeBatchDailyPersistence(t *testing.T) {
	if os.Getenv("COZYSOC_BATCH_PROTOTYPE") == "" {
		t.Skip("set COZYSOC_BATCH_PROTOTYPE=1")
	}
	if os.Getenv("COZYSOC_BATCH_PROTOTYPE") != "1" {
		t.Fatal("COZYSOC_BATCH_PROTOTYPE must be exactly 1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dir := t.TempDir()
	s := openPrototypeBatchStore(t, dir)
	start := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	fixture := &compressionIdentityFixture{devices: map[string]domain.Device{}, lastMAC: map[string]string{}}
	reconciler, err := NewReconciler(fixture)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.New()
	wallStart := time.Now()
	for minute := 0; minute < 1440; minute++ {
		for i := 0; i < 100; i++ {
			mac, err := net.ParseMAC(fmt.Sprintf("02:00:00:00:00:%02x", i+1))
			if err != nil {
				t.Fatal(err)
			}
			obs, err := buildNeighborObservation("scope.storage-lab", "sensor.storage-lab", start.Add(time.Duration(minute)*time.Minute), Neighbor{Address: netip.MustParseAddr(fmt.Sprintf("192.168.50.%d", i+10)), HardwareAddr: mac, InterfaceName: "fixture0", Method: MethodARPCache})
			if err != nil {
				t.Fatal(err)
			}
			fixture.current = compressionEvidence{Observation: obs}
			if _, err := reconciler.ReconcileObservation(ctx, obs); err != nil {
				t.Fatal(err)
			}
			e := fixture.current
			if err := s.Put(ctx, e, start.Add(31*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(prototypeRecord{Evidence: e, Payload: e.Observation.Payload})
			if err != nil {
				t.Fatal(err)
			}
			expected.Write(raw)
		}
		if (minute+1)%60 == 0 {
			t.Logf("persistent prototype: %d/1440 collections", minute+1)
		}
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s = openPrototypeBatchStore(t, dir)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM batches ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if len(ids) > 144000 {
			t.Fatal("excess batches")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	actual := sha256.New()
	total := 0
	for _, id := range ids {
		var blob []byte
		if err := s.db.QueryRowContext(ctx, `SELECT data FROM batches WHERE id=?`, id).Scan(&blob); err != nil {
			t.Fatal(err)
		}
		entries, err := decodePrototypeBatch(blob)
		if err != nil {
			t.Fatal(err)
		}
		indexes, err := s.db.QueryContext(ctx, `SELECT id,slot FROM evidence_index WHERE batch_id=? ORDER BY slot LIMIT 101`, id)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for indexes.Next() {
			var key []byte
			var slot int
			if err := indexes.Scan(&key, &slot); err != nil {
				t.Fatal(err)
			}
			evidenceID, err := prototypeIndexID(key)
			if err != nil {
				t.Fatal(err)
			}
			if slot != n || n >= len(entries) || entries[n].Observation.ID != evidenceID {
				t.Fatal("lookup index differs from durable batch")
			}
			n++
		}
		if err := indexes.Err(); err != nil {
			t.Fatal(err)
		}
		indexes.Close()
		if n != len(entries) {
			t.Fatal("missing lookup entries")
		}
		for _, e := range entries {
			raw, err := json.Marshal(prototypeRecord{Evidence: e, Payload: e.Observation.Payload})
			if err != nil {
				t.Fatal(err)
			}
			actual.Write(raw)
			total++
		}
	}
	if total != 144000 || !bytes.Equal(expected.Sum(nil), actual.Sum(nil)) {
		t.Fatal("acknowledged evidence changed after reopen")
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	allocation := readStorageAllocation(t, filepath.Join(dir, "batch-prototype.db"))
	info, err := os.Stat(filepath.Join(dir, "batch-prototype.db"))
	if err != nil {
		t.Fatal(err)
	}
	if allocation.PageCount*allocation.PageSize != info.Size() {
		t.Fatal("prototype file size differs from page allocation")
	}
	report := struct {
		Kind                   string            `json:"kind"`
		Observations           int               `json:"observations"`
		Batches                int               `json:"batches"`
		DatabaseBytes          int64             `json:"database_bytes"`
		WallMilliseconds       int64             `json:"wall_ms"`
		ReopenedEvidenceSHA256 string            `json:"reopened_evidence_sha256"`
		Allocation             storageAllocation `json:"allocation"`
	}{"standalone-persistence-prototype-not-controller-budget", total, len(ids), info.Size(), time.Since(wallStart).Milliseconds(), hex.EncodeToString(actual.Sum(nil)), allocation}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("batch-prototype-report: %s", raw)
}
