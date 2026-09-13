package devicewatch

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func prototypeEvidenceFixture(t *testing.T, minute int) compressionEvidence {
	t.Helper()
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	obs, err := buildNeighborObservation("scope.prototype", "sensor.prototype", time.Date(2026, 9, 13, 0, minute, 0, 0, time.UTC), Neighbor{Address: netip.MustParseAddr("192.168.50.10"), HardwareAddr: mac, InterfaceName: "fixture0", Method: MethodARPCache})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &compressionIdentityFixture{devices: map[string]domain.Device{}, lastMAC: map[string]string{}, current: compressionEvidence{Observation: obs}}
	reconciler, err := NewReconciler(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.ReconcileObservation(context.Background(), obs); err != nil {
		t.Fatal(err)
	}
	return fixture.current
}
func TestPrototypeBatchCodecPreservesFullEvidence(t *testing.T) {
	for _, noncanonical := range []bool{false, true} {
		e := prototypeEvidenceFixture(t, 0)
		e.Observation.Payload = json.RawMessage(" \n" + string(e.Observation.Payload) + "\t ")
		if noncanonical {
			e.Claims[0].ID = "claim.custom"
			e.Links[0].ClaimID = "claim.custom"
			e.Links[0].ID = "link.custom"
		}
		encoded, err := encodePrototypeBatch([]compressionEvidence{e})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := decodePrototypeBatch(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if len(actual) != 1 || !reflect.DeepEqual(actual[0], e) {
			t.Fatal("codec changed evidence or payload bytes")
		}
	}
}
func prototypeGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestPrototypeBatchCodecRejectsInvalidInput(t *testing.T) {
	good, err := encodePrototypeBatch([]compressionEvidence{prototypeEvidenceFixture(t, 0)})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), good...)
	corrupt[len(corrupt)-1] ^= 1
	cases := map[string][]byte{
		"empty": nil, "truncated": good[:len(good)-1], "checksum": corrupt,
		"encoded limit":     make([]byte, prototypeByteLimit+1),
		"decoded limit":     prototypeGzip(t, []byte(strings.Repeat("x", prototypeByteLimit+1))),
		"trailing stream":   append(append([]byte(nil), good...), good...),
		"unknown version":   prototypeGzip(t, []byte(`{"version":2,"records":[{}]}`)),
		"duplicate version": prototypeGzip(t, []byte(`{"version":1,"version":1,"records":[{}]}`)),
		"unknown field":     prototypeGzip(t, []byte(`{"version":1,"records":[{}],"other":1}`)),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodePrototypeBatch(data); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	if _, err := encodePrototypeBatch(make([]compressionEvidence, prototypeBatchLimit+1)); err == nil {
		t.Fatal("excess record count accepted")
	}
}
func TestPrototypeBatchPartialWriteReopenReplayAndExpiry(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openPrototypeBatchStore(t, dir)
	e := prototypeEvidenceFixture(t, 0)
	expiry := e.Observation.IngestedAt.Add(30 * 24 * time.Hour)
	if err := s.Put(ctx, e, expiry); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s = openPrototypeBatchStore(t, dir)
	got, err := s.Get(ctx, e.Observation.ID, expiry.Add(-time.Nanosecond))
	if err != nil || !reflect.DeepEqual(e, got) {
		t.Fatal("partial batch did not survive reopen", err)
	}
	if err := s.Put(ctx, e, expiry); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM evidence_index`).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated evidence", err)
	}
	changed := e
	changed.Observation.Payload = json.RawMessage(`{"changed":true}`)
	if err := s.Put(ctx, changed, expiry); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	if _, err := s.Get(ctx, e.Observation.ID, expiry); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired evidence visible", err)
	}
	second := prototypeEvidenceFixture(t, 1)
	if err := s.Put(ctx, second, expiry); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM batches`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial batch was not continued", err)
	}
	got, err = s.Get(ctx, e.Observation.ID, expiry.Add(-time.Second))
	if err != nil || !reflect.DeepEqual(e, got) {
		t.Fatal("later append changed earlier evidence", err)
	}
}
func TestPrototypeBatchIndexFailureRollsBackPayload(t *testing.T) {
	ctx := context.Background()
	s := openPrototypeBatchStore(t, t.TempDir())
	first := prototypeEvidenceFixture(t, 0)
	expiry := first.Observation.IngestedAt.Add(time.Hour)
	if err := s.Put(ctx, first, expiry); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := s.db.QueryRow(`SELECT data FROM batches`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_index BEFORE INSERT ON evidence_index BEGIN SELECT RAISE(ABORT,'injected index failure'); END;`); err != nil {
		t.Fatal(err)
	}
	second := prototypeEvidenceFixture(t, 1)
	if err := s.Put(ctx, second, expiry); err == nil {
		t.Fatal("failed transaction was acknowledged")
	}
	var after []byte
	if err := s.db.QueryRow(`SELECT data FROM batches`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("payload update escaped failed index transaction")
	}
	if _, err := s.Get(ctx, second.Observation.ID, first.Observation.IngestedAt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed write became visible", err)
	}
}

func TestPrototypeBatchCrashWriter(t *testing.T) {
	dir := os.Getenv("COZYSOC_BATCH_CRASH_FIXTURE")
	if dir == "" {
		t.Skip("subprocess-only fixture")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "batch-prototype.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE batches SET data=x'626164' WHERE id=1;
 INSERT INTO evidence_index(id,batch_id,slot,expires_at_ns) VALUES('obs.uncommitted',1,1,1);`); err != nil {
		t.Fatal(err)
	}
	// Parent terminates this process after both uncommitted changes exist.
	os.Stdout.WriteString("uncommitted-ready\n")
	time.Sleep(time.Minute)
	t.Fatal("parent did not terminate crash fixture")
}
func TestPrototypeBatchRecoversInterruptedTransaction(t *testing.T) {
	dir := t.TempDir()
	s := openPrototypeBatchStore(t, dir)
	e := prototypeEvidenceFixture(t, 0)
	expiry := e.Observation.IngestedAt.Add(time.Hour)
	if err := s.Put(context.Background(), e, expiry); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrototypeBatchCrashWriter$", "-test.count=1")
	cmd.Env = append(os.Environ(), "COZYSOC_BATCH_CRASH_FIXTURE="+dir)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "uncommitted-ready\n" {
		t.Fatalf("crash fixture not ready: %q %v", line, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("crash fixture exited normally")
	}
	s = openPrototypeBatchStore(t, dir)
	got, err := s.Get(context.Background(), e.Observation.ID, e.Observation.IngestedAt)
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatal("committed evidence lost across interrupted transaction", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM evidence_index`).Scan(&count); err != nil || count != 1 {
		t.Fatal("uncommitted lookup survived", err)
	}
}

func TestPrototypeBatchRollover(t *testing.T) {
	s := openPrototypeBatchStore(t, t.TempDir())
	ctx := context.Background()
	expiry := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	for i := 0; i < prototypeBatchLimit+1; i++ {
		if err := s.Put(ctx, prototypeEvidenceFixture(t, i), expiry); err != nil {
			t.Fatal(err)
		}
	}
	var batches, maxEntries, count int
	if err := s.db.QueryRow(`SELECT count(*),max(entries),sum(entries) FROM batches`).Scan(&batches, &maxEntries, &count); err != nil {
		t.Fatal(err)
	}
	if batches != 2 || maxEntries != prototypeBatchLimit || count != prototypeBatchLimit+1 {
		t.Fatal("batch rollover failed")
	}
	var journal string
	var synchronous int
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if journal != "delete" || synchronous != 2 {
		t.Fatal("prototype durability settings changed")
	}
}

func TestPrototypeBatchIndexKeysPreserveIDs(t *testing.T) {
	ids := []string{"obs.dw.0123456789abcdef0123456789abcdef", "obs.dw.0123456789ABCDEF0123456789ABCDEF", "obs.custom", "obs.dw.not-a-digest"}
	seen := map[string]bool{}
	for _, id := range ids {
		key := prototypeIndexKey(id)
		if seen[string(key)] {
			t.Fatal("different IDs share key")
		}
		seen[string(key)] = true
		decoded, err := prototypeIndexID(key)
		if err != nil || decoded != id {
			t.Fatal("index key changed ID", err)
		}
	}
	if len(prototypeIndexKey(ids[0])) != 17 {
		t.Fatal("canonical ID was not packed")
	}
	for _, key := range [][]byte{nil, {1}, {1, 2}, {2, 3}} {
		if _, err := prototypeIndexID(key); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
