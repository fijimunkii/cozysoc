package storage

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	EvidenceBatchMaxRecords      = 100
	EvidenceBatchMaxBytes        = 1 << 20
	EvidenceBatchMaxAssociations = 8
	evidenceBatchFormat          = "cozysoc-evidence-batch"
)

var (
	ErrEvidenceBatchLimit  = errors.New("evidence batch exceeds a size or count limit")
	ErrEvidenceBatchFormat = errors.New("invalid or unsupported evidence batch encoding")
	ErrEvidenceBatchData   = errors.New("invalid evidence batch record")
)

// RetainedIdentityClaim carries the stored expiry, never a newly calculated TTL.
type RetainedIdentityClaim struct {
	Claim     domain.IdentityClaim `json:"claim"`
	ExpiresAt time.Time            `json:"expires_at"`
}

// EvidenceBatchRecord preserves independently retained evidence. Observation may
// be nil after its payload expires while claims/links remain. The caller applies
// retention and foreign-key semantics; encoding/decoding never changes evidence.
// This codec does not activate a new storage schema or alter existing readers.
type EvidenceBatchRecord struct {
	Observation          *domain.Observation      `json:"observation"`
	ObservationExpiresAt *time.Time               `json:"observation_expires_at"`
	Claims               []RetainedIdentityClaim  `json:"claims"`
	Links                []domain.DeviceClaimLink `json:"links"`
}
type evidenceBatchWireRecord struct {
	Record     EvidenceBatchRecord `json:"record"`
	Payload    []byte              `json:"payload"`
	DerivedIDs bool                `json:"derived_ids"`
}
type evidenceBatchEnvelope struct {
	Format  string                    `json:"format"`
	Version int                       `json:"version"`
	Records []evidenceBatchWireRecord `json:"records"`
}

func validateEvidenceBatchRecord(r EvidenceBatchRecord) error {
	if len(r.Claims) > EvidenceBatchMaxAssociations || len(r.Links) > EvidenceBatchMaxAssociations {
		return ErrEvidenceBatchLimit
	}
	if r.Observation == nil {
		if r.ObservationExpiresAt != nil || len(r.Claims)+len(r.Links) == 0 {
			return ErrEvidenceBatchData
		}
	} else {
		if len(r.Observation.Payload) > domain.MaxJSONBytes {
			return ErrEvidenceBatchLimit
		}
		if r.ObservationExpiresAt == nil || r.ObservationExpiresAt.IsZero() || domain.ValidateObservation(*r.Observation) != nil {
			return ErrEvidenceBatchData
		}
	}
	for _, c := range r.Claims {
		if c.ExpiresAt.IsZero() || domain.ValidateIdentityClaim(c.Claim) != nil {
			return ErrEvidenceBatchData
		}
	}
	for _, l := range r.Links {
		if domain.ValidateDeviceClaimLink(l) != nil {
			return ErrEvidenceBatchData
		}
	}
	return nil
}

// EncodeEvidenceBatch bounds each entry before adding it to the bounded frame.
// It preserves noncanonical identifiers in full and never mutates input records.
func EncodeEvidenceBatch(records []EvidenceBatchRecord) ([]byte, error) {
	if len(records) == 0 || len(records) > EvidenceBatchMaxRecords {
		return nil, ErrEvidenceBatchLimit
	}
	var raw bytes.Buffer
	raw.WriteString(`{"format":"cozysoc-evidence-batch","version":1,"records":[`)
	for i, record := range records {
		if err := validateEvidenceBatchRecord(record); err != nil {
			return nil, err
		}
		wire := evidenceBatchWireRecord{Record: record}
		if record.Observation != nil {
			observation := *record.Observation
			wire.Payload = append([]byte(nil), observation.Payload...)
			observation.Payload = nil
			wire.Record.Observation = &observation
			if canDeriveEvidenceBatchIDs(record) {
				wire.DerivedIDs = true
				wire.Record.Claims = append([]RetainedIdentityClaim(nil), record.Claims...)
				wire.Record.Links = append([]domain.DeviceClaimLink(nil), record.Links...)
				for n := range wire.Record.Claims {
					wire.Record.Claims[n].Claim.ID = ""
					wire.Record.Links[n].ID = ""
					wire.Record.Links[n].ClaimID = ""
				}
			}
		}
		entry, err := json.Marshal(wire)
		if err != nil {
			return nil, ErrEvidenceBatchData
		}
		if raw.Len()+len(entry)+3 > EvidenceBatchMaxBytes {
			return nil, ErrEvidenceBatchLimit
		}
		if i > 0 {
			raw.WriteByte(',')
		}
		raw.Write(entry)
	}
	raw.WriteString(`]}`)
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(raw.Bytes()); err != nil {
		return nil, ErrEvidenceBatchFormat
	}
	if err := writer.Close(); err != nil {
		return nil, ErrEvidenceBatchFormat
	}
	if encoded.Len() > EvidenceBatchMaxBytes {
		return nil, ErrEvidenceBatchLimit
	}
	return encoded.Bytes(), nil
}

// DecodeEvidenceBatch accepts only versioned canonical frames, bounds expansion,
// and checks gzip integrity. It returns no partial records on any failure.
func DecodeEvidenceBatch(encoded []byte) ([]EvidenceBatchRecord, error) {
	if len(encoded) == 0 {
		return nil, ErrEvidenceBatchFormat
	}
	if len(encoded) > EvidenceBatchMaxBytes {
		return nil, ErrEvidenceBatchLimit
	}
	source := bytes.NewReader(encoded)
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, ErrEvidenceBatchFormat
	}
	reader.Multistream(false)
	raw, readErr := io.ReadAll(io.LimitReader(reader, EvidenceBatchMaxBytes+1))
	closeErr := reader.Close()
	if len(raw) > EvidenceBatchMaxBytes {
		return nil, ErrEvidenceBatchLimit
	}
	if readErr != nil || closeErr != nil || source.Len() != 0 {
		return nil, ErrEvidenceBatchFormat
	}
	if err := boundEvidenceBatchStructure(raw); err != nil {
		return nil, err
	}
	var envelope evidenceBatchEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, ErrEvidenceBatchFormat
	}
	if envelope.Format != evidenceBatchFormat || envelope.Version != 1 {
		return nil, ErrEvidenceBatchFormat
	}
	if len(envelope.Records) == 0 || len(envelope.Records) > EvidenceBatchMaxRecords {
		return nil, ErrEvidenceBatchLimit
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrEvidenceBatchFormat
	}
	records := make([]EvidenceBatchRecord, len(envelope.Records))
	for i, wire := range envelope.Records {
		record := wire.Record
		if record.Observation == nil {
			if wire.Payload != nil || wire.DerivedIDs {
				return nil, ErrEvidenceBatchFormat
			}
		} else {
			if !bytes.Equal(record.Observation.Payload, []byte("null")) {
				return nil, ErrEvidenceBatchFormat
			}
			record.Observation.Payload = append(json.RawMessage(nil), wire.Payload...)
			if wire.DerivedIDs {
				if len(record.Claims) != 2 || len(record.Links) != 2 {
					return nil, ErrEvidenceBatchFormat
				}
				for n := range record.Claims {
					if record.Claims[n].Claim.ID != "" || record.Links[n].ID != "" || record.Links[n].ClaimID != "" {
						return nil, ErrEvidenceBatchFormat
					}
					claimID, linkID, ok := derivedEvidenceBatchIDs(record, n)
					if !ok {
						return nil, ErrEvidenceBatchFormat
					}
					record.Claims[n].Claim.ID = claimID
					record.Links[n].ID = linkID
					record.Links[n].ClaimID = claimID
				}
			}
		}
		if err := validateEvidenceBatchRecord(record); err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}

func canDeriveEvidenceBatchIDs(r EvidenceBatchRecord) bool {
	if r.Observation == nil || len(r.Claims) != 2 || len(r.Links) != 2 {
		return false
	}
	for i := range r.Claims {
		claimID, linkID, ok := derivedEvidenceBatchIDs(r, i)
		if !ok || r.Claims[i].Claim.ID != claimID || r.Links[i].ID != linkID || r.Links[i].ClaimID != claimID {
			return false
		}
	}
	return true
}
func derivedEvidenceBatchIDs(r EvidenceBatchRecord, i int) (string, string, bool) {
	var prefix, tag string
	switch r.Claims[i].Claim.Kind {
	case domain.ClaimMAC:
		prefix, tag = "claim.dw.mac.", "mac-claim-v1"
	case domain.ClaimIPv4, domain.ClaimIPv6:
		prefix, tag = "claim.dw.ip.", "ip-claim-v1"
	default:
		return "", "", false
	}
	claimID := prefix + evidenceBatchDigestV1(tag, r.Observation.ID, r.Claims[i].Claim.Value)
	return claimID, "link.dw." + evidenceBatchDigestV1("link-v1", r.Links[i].DeviceID, claimID), true
}

// Frozen codec-v1 derivation, matching Device Watch's existing IDs. Future
// producer changes must retain this decoder and select a new encoding version.
func evidenceBatchDigestV1(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

// Reject large arrays and deeply nested invalid JSON before decoding it into
// potentially much larger Go record structs. The byte limit alone is not enough
// to bound allocations for arrays containing many tiny objects.
func boundEvidenceBatchStructure(raw []byte) error {
	type frame struct {
		kind  json.Delim
		count int
	}
	var stack []frame
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return ErrEvidenceBatchFormat
		}
		delimiter, isDelimiter := token.(json.Delim)
		if isDelimiter && (delimiter == '}' || delimiter == ']') {
			if len(stack) == 0 {
				return ErrEvidenceBatchFormat
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if len(stack) > 0 && stack[len(stack)-1].kind == '[' {
			top := &stack[len(stack)-1]
			top.count++
			if top.count > EvidenceBatchMaxRecords {
				return ErrEvidenceBatchLimit
			}
		}
		if isDelimiter {
			if len(stack) >= 32 {
				return ErrEvidenceBatchLimit
			}
			stack = append(stack, frame{kind: delimiter})
		}
	}
}
