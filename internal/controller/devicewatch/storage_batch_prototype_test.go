package devicewatch

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// Test-only persistent prototype for #150. It never opens controller state.
const prototypeBatchLimit = 100
const prototypeByteLimit = 1 << 20

type prototypeRecord struct {
	Evidence   compressionEvidence `json:"evidence"`
	Payload    []byte              `json:"payload"` // preserve original JSON whitespace/bytes
	DerivedIDs bool                `json:"derived_ids"`
}
type prototypeEnvelope struct {
	Version int               `json:"version"`
	Records []prototypeRecord `json:"records"`
}

func validatePrototypeEvidence(e compressionEvidence) error {
	if len(e.Observation.Payload) > domain.MaxJSONBytes {
		return fmt.Errorf("observation payload too large")
	}
	if len(e.Claims) > 8 || len(e.Links) > 8 {
		return fmt.Errorf("too many evidence associations")
	}
	if err := domain.ValidateObservation(e.Observation); err != nil {
		return err
	}
	for _, c := range e.Claims {
		if err := domain.ValidateIdentityClaim(c); err != nil {
			return err
		}
	}
	for _, l := range e.Links {
		if err := domain.ValidateDeviceClaimLink(l); err != nil {
			return err
		}
	}
	return nil
}
func encodePrototypeBatch(entries []compressionEvidence) ([]byte, error) {
	if len(entries) == 0 || len(entries) > prototypeBatchLimit {
		return nil, fmt.Errorf("invalid batch count")
	}
	envelope := prototypeEnvelope{Version: 1, Records: make([]prototypeRecord, len(entries))}
	for i, e := range entries {
		if err := validatePrototypeEvidence(e); err != nil {
			return nil, err
		}
		wire := prototypeRecord{Evidence: e, Payload: append([]byte(nil), e.Observation.Payload...)}
		if packed, err := packDerivedEvidenceIDs([]compressionEvidence{e}); err == nil {
			wire.Evidence = packed[0]
			wire.DerivedIDs = true
		}
		wire.Evidence.Observation.Payload = nil
		envelope.Records[i] = wire
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(raw) > prototypeByteLimit {
		return nil, fmt.Errorf("decoded batch limit exceeded")
	}
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if out.Len() > prototypeByteLimit {
		return nil, fmt.Errorf("encoded batch limit exceeded")
	}
	return out.Bytes(), nil
}
func decodePrototypeBatch(encoded []byte) ([]compressionEvidence, error) {
	if len(encoded) == 0 || len(encoded) > prototypeByteLimit {
		return nil, fmt.Errorf("invalid encoded batch length")
	}
	source := bytes.NewReader(encoded)
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, err
	}
	reader.Multistream(false)
	raw, err := io.ReadAll(io.LimitReader(reader, prototypeByteLimit+1))
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(raw) > prototypeByteLimit || source.Len() != 0 {
		return nil, fmt.Errorf("oversized or trailing batch data")
	}
	var envelope prototypeEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Version != 1 || len(envelope.Records) == 0 || len(envelope.Records) > prototypeBatchLimit {
		return nil, fmt.Errorf("unsupported batch version or count")
	}
	// Only canonical encoder output is accepted, rejecting duplicate fields,
	// trailing JSON, alternate field spellings and ambiguous representations.
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("noncanonical batch envelope")
	}
	result := make([]compressionEvidence, len(envelope.Records))
	for i, wire := range envelope.Records {
		if !bytes.Equal(wire.Evidence.Observation.Payload, []byte("null")) {
			return nil, fmt.Errorf("duplicate observation payload")
		}
		e := wire.Evidence
		if wire.DerivedIDs {
			one := []compressionEvidence{e}
			if err := restoreDerivedEvidenceIDs(one); err != nil {
				return nil, err
			}
			e = one[0]
		}
		e.Observation.Payload = append(json.RawMessage(nil), wire.Payload...)
		if err := validatePrototypeEvidence(e); err != nil {
			return nil, err
		}
		result[i] = e
	}
	return result, nil
}

type prototypeBatchStore struct{ db *sql.DB }

func openPrototypeBatchStore(t *testing.T, dir string) *prototypeBatchStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "batch-prototype.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = db.Exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS batches(id INTEGER PRIMARY KEY, entries INTEGER NOT NULL CHECK(entries BETWEEN 1 AND 100), data BLOB NOT NULL CHECK(length(data) BETWEEN 1 AND 1048576));
 CREATE TABLE IF NOT EXISTS evidence_index(id BLOB PRIMARY KEY, batch_id INTEGER NOT NULL REFERENCES batches(id), slot INTEGER NOT NULL, expires_at_ns INTEGER NOT NULL) WITHOUT ROWID;`)
	if err != nil {
		t.Fatal(err)
	}
	return &prototypeBatchStore{db: db}
}

// Every successful call commits both the updated compressed batch and lookup
// entry. There is no minute-long buffer and no acknowledgment before COMMIT.
func (s *prototypeBatchStore) Put(ctx context.Context, e compressionEvidence, expiry time.Time) error {
	if err := validatePrototypeEvidence(e); err != nil {
		return err
	}
	if expiry.IsZero() {
		return fmt.Errorf("missing expiry")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldData []byte
	var oldSlot int
	var oldExpiry int64
	err = tx.QueryRowContext(ctx, `SELECT CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,i.slot,i.expires_at_ns FROM evidence_index i JOIN batches b ON b.id=i.batch_id WHERE i.id=?`, prototypeIndexKey(e.Observation.ID)).Scan(&oldData, &oldSlot, &oldExpiry)
	if err == nil {
		entries, err := decodePrototypeBatch(oldData)
		if err != nil {
			return err
		}
		if oldSlot < 0 || oldSlot >= len(entries) {
			return fmt.Errorf("corrupt evidence slot")
		}
		a, _ := json.Marshal(prototypeRecord{Evidence: entries[oldSlot], Payload: entries[oldSlot].Observation.Payload})
		b, _ := json.Marshal(prototypeRecord{Evidence: e, Payload: e.Observation.Payload})
		if !bytes.Equal(a, b) || oldExpiry != expiry.UnixNano() {
			return fmt.Errorf("conflicting evidence replay")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var batchID int64
	var blob []byte
	var count int
	err = tx.QueryRowContext(ctx, `SELECT id,CASE WHEN length(data)<=1048576 THEN data ELSE NULL END,entries FROM batches ORDER BY id DESC LIMIT 1`).Scan(&batchID, &blob, &count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var entries []compressionEvidence
	if err == nil && count < prototypeBatchLimit {
		entries, err = decodePrototypeBatch(blob)
		if err != nil {
			return err
		}
		if len(entries) != count {
			return fmt.Errorf("corrupt batch count")
		}
	} else {
		batchID = 0
	}
	entries = append(entries, e)
	encoded, err := encodePrototypeBatch(entries)
	if err != nil && len(entries) > 1 {
		batchID = 0
		entries = []compressionEvidence{e}
		encoded, err = encodePrototypeBatch(entries)
	}
	if err != nil {
		return err
	}
	if batchID == 0 {
		result, err := tx.ExecContext(ctx, `INSERT INTO batches(entries,data) VALUES(?,?)`, len(entries), encoded)
		if err != nil {
			return err
		}
		batchID, err = result.LastInsertId()
		if err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE batches SET entries=?,data=? WHERE id=?`, len(entries), encoded, batchID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evidence_index(id,batch_id,slot,expires_at_ns) VALUES(?,?,?,?)`, prototypeIndexKey(e.Observation.ID), batchID, len(entries)-1, expiry.UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *prototypeBatchStore) Get(ctx context.Context, id string, now time.Time) (compressionEvidence, error) {
	if len(id) == 0 || len(id) > 128 {
		return compressionEvidence{}, fmt.Errorf("invalid lookup ID length")
	}
	var blob []byte
	var slot int
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN length(b.data)<=1048576 THEN b.data ELSE NULL END,i.slot FROM evidence_index i JOIN batches b ON b.id=i.batch_id WHERE i.id=? AND i.expires_at_ns>?`, prototypeIndexKey(id), now.UnixNano()).Scan(&blob, &slot)
	if err != nil {
		return compressionEvidence{}, err
	}
	entries, err := decodePrototypeBatch(blob)
	if err != nil {
		return compressionEvidence{}, err
	}
	if slot < 0 || slot >= len(entries) || entries[slot].Observation.ID != id {
		return compressionEvidence{}, fmt.Errorf("corrupt indexed evidence")
	}
	return entries[slot], nil
}

// Tagged, reversible index keys. Full IDs never collide with packed canonical
// IDs; noncanonical spelling (including uppercase hex) is preserved verbatim.
func prototypeIndexKey(id string) []byte {
	if strings.HasPrefix(id, "obs.dw.") && len(id) == 39 {
		raw, err := hex.DecodeString(id[7:])
		if err == nil && hex.EncodeToString(raw) == id[7:] {
			return append([]byte{1}, raw...)
		}
	}
	return append([]byte{0}, []byte(id)...)
}
func prototypeIndexID(key []byte) (string, error) {
	if len(key) < 2 || len(key) > 129 {
		return "", fmt.Errorf("invalid index key")
	}
	switch key[0] {
	case 0:
		id := string(key[1:])
		if !bytes.Equal(prototypeIndexKey(id), key) {
			return "", fmt.Errorf("noncanonical index key")
		}
		return id, nil
	case 1:
		if len(key) == 17 {
			return "obs.dw." + hex.EncodeToString(key[1:]), nil
		}
	}
	return "", fmt.Errorf("invalid index key")
}
