package storage

import (
	"context"
	"database/sql"
	"path/filepath"
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

func TestArrivalAcknowledgementSchemaUpgradesRetainedV6Finding(t *testing.T) {
	dir := t.TempDir()
	dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(migrationV1 + migrationV2 + migrationV3 + migrationV4 + migrationV5 + migrationV6 + `
		INSERT INTO network_scopes VALUES('scope.home','lan',1,NULL,'{}');
		INSERT INTO findings(id,scope_id,detector_id,detector_version,category,severity,observed_at_ns,created_at_ns,schema_version,payload,retention_class,expires_at_ns)
		VALUES('finding.one','scope.home','device-watch-arrival','1','new-device','informational',1,1,1,'{}','standard',999999999999999999);
		PRAGMA user_version=6;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var acknowledged sql.NullInt64
	if err := s.conn.QueryRowContext(context.Background(), `SELECT acknowledged_at_ns FROM findings WHERE id='finding.one'`).Scan(&acknowledged); err != nil || acknowledged.Valid {
		t.Fatal("existing finding was not preserved as unreviewed", acknowledged, err)
	}
}

func TestArrivalAcknowledgementIsIdempotentAndAuditedAtomically(t *testing.T) {
	s, db := detailBatchFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 13, 12, 30, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.InsertFinding(ctx, domain.Finding{ID: "finding.dw.arrival.one", ScopeID: "scope.fixture", DetectorID: "device-watch-arrival", DetectorVersion: "1",
		Category: "new-device", Severity: "informational", ObservedAt: now, CreatedAt: now,
		SchemaVersion: 1, Payload: []byte(`{}`), EvidenceObservationIDs: []string{"obs.fixture"}, Retention: domain.RetentionStandard}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcknowledgeArrivalFinding(ctx, "finding.unknown"); err != ErrArrivalFindingNotFound {
		t.Fatalf("unknown finding: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_ack_audit BEFORE INSERT ON audit_events WHEN NEW.kind='finding-acknowledgement' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcknowledgeArrivalFinding(ctx, "finding.dw.arrival.one"); err == nil {
		t.Fatal("audit failure acknowledged finding")
	}
	var stored any
	if err := db.QueryRow(`SELECT acknowledged_at_ns FROM findings WHERE id='finding.dw.arrival.one'`).Scan(&stored); err != nil || stored != nil {
		t.Fatal("acknowledgement was not rolled back", stored, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_ack_audit`); err != nil {
		t.Fatal(err)
	}
	at, changed, err := s.AcknowledgeArrivalFinding(ctx, "finding.dw.arrival.one")
	if err != nil || !changed || !at.Equal(now) {
		t.Fatal(at, changed, err)
	}
	now = now.Add(time.Minute)
	at, changed, err = s.AcknowledgeArrivalFinding(ctx, "finding.dw.arrival.one")
	if err != nil || changed || !at.Equal(now.Add(-time.Minute)) {
		t.Fatal(at, changed, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE kind='finding-acknowledgement' AND actor='local-os-user'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("acknowledgement audit count", count, err)
	}
	page, err := s.ListRecentArrivalFindings(ctx, now)
	if err != nil || len(page.Findings) != 1 || page.Findings[0].AcknowledgedAt == nil || !page.Findings[0].AcknowledgedAt.Equal(at) {
		t.Fatal(page, err)
	}
}
