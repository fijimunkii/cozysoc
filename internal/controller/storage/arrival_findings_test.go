package storage

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestRecentArrivalFindingsRetainSourceStatusWithoutPayload(t *testing.T) {
	s, db := detailBatchFixture(t)
	ctx := context.Background()
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	asOf := time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC)
	s.now = func() time.Time { return asOf }
	f := domain.Finding{ID: "finding.dw.arrival.fixture", ScopeID: "scope.fixture", DetectorID: "device-watch-arrival", DetectorVersion: "1",
		Category: "new-device", Severity: "informational", ObservedAt: r.Claims[0].Claim.ObservedAt, CreatedAt: asOf,
		SchemaVersion: 1, Payload: []byte(`{"private":"must-not-leak"}`), EvidenceObservationIDs: []string{r.Observation.ID}, Retention: domain.RetentionStandard}
	if err := s.InsertFinding(ctx, f); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListRecentArrivalFindings(ctx, asOf)
	if err != nil || page.Truncated || len(page.Findings) != 1 || !page.Findings[0].EvidenceRetained || page.Findings[0].EvidenceObservationID != r.Observation.ID {
		t.Fatal(page, err)
	}
	if _, err := db.Exec(`DELETE FROM evidence_batch_lookup WHERE id=?`, packedEvidenceKey(r.Observation.ID, "obs.dw.")); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListRecentArrivalFindings(ctx, asOf)
	if err != nil || len(page.Findings) != 1 || page.Findings[0].EvidenceRetained {
		t.Fatal(page, err)
	}
	page, err = s.ListRecentArrivalFindings(ctx, asOf.Add(31*24*time.Hour))
	if err != nil || len(page.Findings) != 0 {
		t.Fatal(page, err)
	}
}
