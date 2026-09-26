package storage

import (
	"context"
	"testing"
)

func TestInventoryCountsMixedStoredEvidenceWithoutTreatingBatchesAsSQLiteRows(t *testing.T) {
	s, db := detailBatchFixture(t)
	ctx := context.Background()
	initial, err := s.Inventory(ctx)
	if err != nil || initial != (Inventory{LabeledDevices: 1}) {
		t.Fatal(initial, err)
	}
	if ok, err := batchSQLAppend(t, db, detailRecord(1, 0)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	storeLegacyDetailRecord(t, s, detailRecord(2, 0))
	got, err := s.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Inventory{BatchEvidenceRecords: 1, OtherObservations: 1, IdentityClaims: 2, LabeledDevices: 1}) {
		t.Fatalf("inventory = %+v", got)
	}
}
