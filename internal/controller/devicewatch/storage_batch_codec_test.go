package devicewatch

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestStorageEvidenceBatchMatchesDeviceWatchReconciler(t *testing.T) {
	e := prototypeEvidenceFixture(t, 0)
	expiry := e.Observation.IngestedAt.Add(30 * 24 * time.Hour)
	record := storage.EvidenceBatchRecord{Observation: &e.Observation, ObservationExpiresAt: &expiry, Links: e.Links}
	for i, c := range e.Claims {
		record.Claims = append(record.Claims, storage.RetainedIdentityClaim{Claim: c, ExpiresAt: expiry.Add(time.Duration(i) * time.Hour)})
	}
	encoded, err := storage.EncodeEvidenceBatch([]storage.EvidenceBatchRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var frame struct {
		Records []struct {
			DerivedIDs bool `json:"derived_ids"`
		} `json:"records"`
	}
	if err := json.NewDecoder(reader).Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Records) != 1 || !frame.Records[0].DerivedIDs {
		t.Fatal("producer IDs are not compatible with frozen storage derivation")
	}
	decoded, err := storage.DecodeEvidenceBatch(encoded)
	if err != nil || !reflect.DeepEqual(decoded, []storage.EvidenceBatchRecord{record}) {
		t.Fatal("storage codec changed production-generated evidence", err)
	}
}
