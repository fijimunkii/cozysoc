package storage

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func batchRecordFixture() EvidenceBatchRecord {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	expiry := at.Add(time.Hour)
	valid := at.Add(10 * time.Minute)
	confidence := 0.75
	obs := &domain.Observation{ID: "obs.fixture", ScopeID: "scope.fixture", SensorID: "sensor.fixture", Kind: "device-neighbor-seen", SourceStream: "fixture", SourceKey: "source.one", SourceTime: &at, IngestedAt: at.Add(time.Minute), SchemaVersion: 1, Attribution: "fixture", Payload: json.RawMessage(" \n{\"name\": \"lamp\"}\t"), Retention: domain.RetentionStandard}
	r := EvidenceBatchRecord{Observation: obs, ObservationExpiresAt: &expiry}
	for i, kind := range []domain.ClaimKind{domain.ClaimMAC, domain.ClaimIPv4} {
		value := []string{"02:00:00:00:00:01", "192.168.50.10"}[i]
		id := []string{"claim.mac", "claim.ip"}[i]
		r.Claims = append(r.Claims, RetainedIdentityClaim{Claim: domain.IdentityClaim{ID: id, ScopeID: obs.ScopeID, Kind: kind, Value: value, ObservedAt: at, ValidUntil: &valid, Confidence: &confidence, SourceSensorID: obs.SensorID, SourceObservationID: obs.ID, Retention: domain.RetentionStandard}, ExpiresAt: expiry.Add(time.Duration(i+1) * time.Hour)})
		r.Links = append(r.Links, domain.DeviceClaimLink{ID: []string{"link.mac", "link.ip"}[i], DeviceID: "device.fixture", ClaimID: id, ValidFrom: at, ValidUntil: &valid, Confidence: &confidence, Authority: domain.LinkInferred, Reason: "fixture", EvidenceObservationID: obs.ID, CreatedAt: at})
	}
	return r
}
func TestEvidenceBatchPreservesEvidenceAndIndependentExpiry(t *testing.T) {
	full := batchRecordFixture()
	retained := batchRecordFixture()
	retained.Observation = nil
	retained.ObservationExpiresAt = nil
	for i := range retained.Claims {
		retained.Claims[i].Claim.SourceObservationID = ""
		retained.Links[i].EvidenceObservationID = ""
	}
	original := []EvidenceBatchRecord{full, retained}
	before, _ := json.Marshal(original)
	encoded, err := EncodeEvidenceBatch(original)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if !bytes.Equal(before, after) {
		t.Fatal("encoder mutated evidence")
	}
	got, err := DecodeEvidenceBatch(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, got) {
		t.Fatal("evidence, payload bytes, missing observation or independent expiry changed")
	}
	got[0].Observation.Payload[0] = 'x'
	if original[0].Observation.Payload[0] == 'x' {
		t.Fatal("decoded payload aliases input")
	}
}
func TestEvidenceBatchDerivedAndNoncanonicalIDs(t *testing.T) {
	r := batchRecordFixture()
	for i := range r.Claims {
		c, l, _ := derivedEvidenceBatchIDs(r, i)
		r.Claims[i].Claim.ID = c
		r.Links[i].ID = l
		r.Links[i].ClaimID = c
	}
	for _, mutation := range []string{"none", "claim", "link", "reference"} {
		t.Run(mutation, func(t *testing.T) {
			entry := r
			entry.Claims = append([]RetainedIdentityClaim(nil), r.Claims...)
			entry.Links = append([]domain.DeviceClaimLink(nil), r.Links...)
			switch mutation {
			case "claim":
				entry.Claims[0].Claim.ID = "claim.custom"
			case "link":
				entry.Links[0].ID = "link.custom"
			case "reference":
				entry.Links[0].ClaimID = "claim.other"
			}
			encoded, err := EncodeEvidenceBatch([]EvidenceBatchRecord{entry})
			if err != nil {
				t.Fatal(err)
			}
			raw := readBatchFrame(t, encoded)
			var frame evidenceBatchEnvelope
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Records[0].DerivedIDs != (mutation == "none") {
				t.Fatal("incorrect derived-ID choice")
			}
			decoded, err := DecodeEvidenceBatch(encoded)
			if err != nil || !reflect.DeepEqual(decoded, []EvidenceBatchRecord{entry}) {
				t.Fatal("ID reconstruction changed original evidence", err)
			}
		})
	}
}
func readBatchFrame(t *testing.T, encoded []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func batchFrameGzip(t *testing.T, raw []byte) []byte {
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
func TestEvidenceBatchRejectsCorruptionAndAmbiguousFrames(t *testing.T) {
	good, err := EncodeEvidenceBatch([]EvidenceBatchRecord{batchRecordFixture()})
	if err != nil {
		t.Fatal(err)
	}
	raw := readBatchFrame(t, good)
	corrupt := append([]byte(nil), good...)
	corrupt[len(corrupt)-1] ^= 1
	cases := map[string][]byte{
		"empty": nil, "truncated": good[:len(good)-1], "checksum": corrupt,
		"extra stream":    append(append([]byte(nil), good...), good...),
		"unknown format":  batchFrameGzip(t, bytes.Replace(raw, []byte(evidenceBatchFormat), []byte("other-batch"), 1)),
		"unknown version": batchFrameGzip(t, bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":2`), 1)),
		"duplicate field": batchFrameGzip(t, bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)),
		"case alias":      batchFrameGzip(t, bytes.Replace(raw, []byte(`"version"`), []byte(`"Version"`), 1)),
		"unknown field":   batchFrameGzip(t, bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"other":1`), 1)),
		"trailing JSON":   batchFrameGzip(t, append(append([]byte(nil), raw...), []byte(`{}`)...)),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeEvidenceBatch(data)
			if err == nil || got != nil {
				t.Fatal("invalid frame returned records")
			}
		})
	}
}
func TestEvidenceBatchBoundsAndExpiryValidation(t *testing.T) {
	if _, err := EncodeEvidenceBatch(make([]EvidenceBatchRecord, EvidenceBatchMaxRecords+1)); !errors.Is(err, ErrEvidenceBatchLimit) {
		t.Fatal(err)
	}
	records := make([]EvidenceBatchRecord, EvidenceBatchMaxRecords)
	for i := range records {
		records[i] = batchRecordFixture()
		records[i].Observation.Payload = json.RawMessage(`{"value":"` + strings.Repeat("x", domain.MaxJSONBytes-20) + `"}`)
	}
	if _, err := EncodeEvidenceBatch(records); !errors.Is(err, ErrEvidenceBatchLimit) {
		t.Fatal("aggregate frame limit not enforced", err)
	}
	if _, err := DecodeEvidenceBatch(make([]byte, EvidenceBatchMaxBytes+1)); !errors.Is(err, ErrEvidenceBatchLimit) {
		t.Fatal(err)
	}
	if _, err := DecodeEvidenceBatch(batchFrameGzip(t, []byte(strings.Repeat("x", EvidenceBatchMaxBytes+1)))); !errors.Is(err, ErrEvidenceBatchLimit) {
		t.Fatal("expansion limit not enforced", err)
	}
	for _, mutation := range []string{"observation-expiry", "claim-expiry", "orphan-expiry", "associations"} {
		t.Run(mutation, func(t *testing.T) {
			r := batchRecordFixture()
			switch mutation {
			case "observation-expiry":
				r.ObservationExpiresAt = nil
			case "claim-expiry":
				r.Claims[0].ExpiresAt = time.Time{}
			case "orphan-expiry":
				r.Observation = nil
			case "associations":
				r.Links = make([]domain.DeviceClaimLink, EvidenceBatchMaxAssociations+1)
			}
			if _, err := EncodeEvidenceBatch([]EvidenceBatchRecord{r}); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}
func FuzzDecodeEvidenceBatch(f *testing.F) {
	seed, err := EncodeEvidenceBatch([]EvidenceBatchRecord{batchRecordFixture()})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("invalid"))
	f.Fuzz(func(t *testing.T, data []byte) {
		records, err := DecodeEvidenceBatch(data)
		if err != nil {
			if records != nil {
				t.Fatal("partial records on error")
			}
			return
		}
		encoded, err := EncodeEvidenceBatch(records)
		if err != nil {
			t.Fatal("accepted records cannot re-encode", err)
		}
		again, err := DecodeEvidenceBatch(encoded)
		if err != nil || !reflect.DeepEqual(records, again) {
			t.Fatal("accepted frame does not round trip", err)
		}
	})
}

func TestEvidenceBatchV1Golden(t *testing.T) {
	raw, err := os.ReadFile("testdata/evidence_batch_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEvidenceBatch(batchFrameGzip(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	expected := batchRecordFixture()
	claims := []string{"claim.dw.mac.94f94169e0ab3ab729cced220e102299", "claim.dw.ip.23920eb2ded75326cde87a29642ac1fd"}
	links := []string{"link.dw.6c59a3b3af328eb7a0b6ea3fd9f38b79", "link.dw.34268ff048c3849ebda06a6eaff34138"}
	for i := range claims {
		expected.Claims[i].Claim.ID = claims[i]
		expected.Links[i].ID = links[i]
		expected.Links[i].ClaimID = claims[i]
	}
	if !reflect.DeepEqual(decoded, []EvidenceBatchRecord{expected}) {
		t.Fatal("stored v1 evidence no longer decodes identically")
	}
}

func TestEvidenceBatchRejectsStructuralExpansionBeforeRecordDecode(t *testing.T) {
	arrays := []byte(`{"format":"cozysoc-evidence-batch","version":1,"records":[` + strings.Repeat(`{},`, 100) + `{}]}`)
	nested := []byte(strings.Repeat(`[`, 33) + `0` + strings.Repeat(`]`, 33))
	for _, raw := range [][]byte{arrays, nested} {
		if records, err := DecodeEvidenceBatch(batchFrameGzip(t, raw)); records != nil || !errors.Is(err, ErrEvidenceBatchLimit) {
			t.Fatal("structural allocation bound not enforced", err)
		}
	}
}

func FuzzDecodeEvidenceBatchFrame(f *testing.F) {
	seed, err := os.ReadFile("testdata/evidence_batch_v1.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > EvidenceBatchMaxBytes+1 {
			t.Skip()
		}
		records, err := DecodeEvidenceBatch(batchFrameGzip(t, raw))
		if err != nil {
			if records != nil {
				t.Fatal("partial records on error")
			}
			return
		}
		encoded, err := EncodeEvidenceBatch(records)
		if err != nil {
			t.Fatal("accepted frame cannot re-encode", err)
		}
		again, err := DecodeEvidenceBatch(encoded)
		if err != nil || !reflect.DeepEqual(records, again) {
			t.Fatal("accepted frame changed on round trip", err)
		}
	})
}
