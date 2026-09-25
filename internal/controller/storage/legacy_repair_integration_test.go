package storage_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func legacyRepairFixture(t *testing.T) (*storage.Store, *sql.DB, domain.Observation) {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	s, err := storage.Open(t.TempDir(), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.fixture", Kind: "lan", EnrolledAt: at, Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSensor(ctx, domain.Sensor{ID: "sensor.fixture", ScopeID: "scope.fixture", Kind: "desktop-neighbor-cache", Ownership: "builtin", RegisteredAt: at, Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	uri := url.URL{Scheme: "file", Path: s.Path()}
	uri.RawQuery = url.Values{"mode": {"rw"}, "_pragma": {"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(100)"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	o := domain.Observation{ID: "obs.original", ScopeID: "scope.fixture", SensorID: "sensor.fixture", Kind: "device-neighbor-seen", SourceStream: "fixture", SourceKey: "source.original", SourceEventID: "original.event", SourceTime: &at, IngestedAt: at, SchemaVersion: 1, Attribution: "fixture", Retention: domain.RetentionStandard, Payload: json.RawMessage(` {"schema_version":1,"address":"192.168.50.20","hardware_address":"02:00:00:00:00:01","interface":"fixture0","family":"ipv4","method":"arp-cache","state":"reachable"} `)}
	if ok, err := s.InsertObservation(ctx, o); err != nil || !ok {
		t.Fatal(ok, err)
	}
	return s, db, o
}
func repairLegacy(t *testing.T, db *sql.DB, o domain.Observation) (bool, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	repaired, err := devicewatch.RepairLegacyObservation(context.Background(), tx, storage.DefaultLimits(), o)
	if err != nil {
		return false, err
	}
	return repaired, tx.Commit()
}
func legacyRowCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
		t.Fatal(table, count, want, err)
	}
}

func TestLegacyRepairUsesOriginalAndPreservesPartialClaimIDs(t *testing.T) {
	s, db, o := legacyRepairFixture(t)
	ctx := context.Background()
	plan, err := devicewatch.PlanReconciliation(ctx, s, o)
	if err != nil {
		t.Fatal(err)
	}
	partial := plan.Claims[0]
	partial.ID = "claim.preexisting.mac"
	if _, err := s.EnsureIdentityClaim(ctx, partial); err != nil {
		t.Fatal(err)
	}
	var originalExpiry, claimExpiry int64
	if err := db.QueryRow("SELECT expires_at_ns FROM observations WHERE id=?", o.ID).Scan(&originalExpiry); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT expires_at_ns FROM identity_claims WHERE id=?", partial.ID).Scan(&claimExpiry); err != nil {
		t.Fatal(err)
	}
	retry := o
	retry.ID = "obs.changed.retry"
	retry.IngestedAt = o.IngestedAt.Add(time.Hour)
	later := retry.IngestedAt
	retry.SourceTime = &later
	retry.Payload = json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(string(o.Payload), "192.168.50.20", "192.168.50.99"), "02:00:00:00:00:01", "02:00:00:00:00:ff"))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stager, err := storage.NewEvidenceBatchStager(storage.DefaultLimits(), devicewatch.PlanBatchEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := stager.Stage(ctx, tx, retry); !errors.Is(err, storage.ErrEvidenceBatchLegacyReplay) || ok {
		t.Fatal("missing legacy dispatch", ok, err)
	}
	if repaired, err := devicewatch.RepairLegacyObservation(ctx, tx, storage.DefaultLimits(), retry); err != nil || !repaired {
		t.Fatal(repaired, err)
	}
	// The preexisting observation/claim remain committed; new identity is private.
	page, err := s.ListDeviceEvidence(ctx, storage.DeviceEvidenceQuery{ScopeID: o.ScopeID, AsOf: later})
	if err != nil || len(page.Devices) != 0 {
		t.Fatal("repair committed without owner", page, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if repaired, err := repairLegacy(t, db, retry); err != nil || !repaired {
				t.Fatal(repaired, err)
			}
		}
		legacyRowCount(t, db, "observations", 1)
		legacyRowCount(t, db, "identity_claims", 2)
		legacyRowCount(t, db, "device_claim_links", 2)
		legacyRowCount(t, db, "devices", 1)
		legacyRowCount(t, db, "evidence_batches", 0)
		var expiry int64
		var payload string
		if err := db.QueryRow("SELECT expires_at_ns,payload FROM observations WHERE id=?", o.ID).Scan(&expiry, &payload); err != nil || expiry != originalExpiry || payload != string(o.Payload) {
			t.Fatal("original observation changed", expiry, err)
		}
		if err := db.QueryRow("SELECT expires_at_ns FROM identity_claims WHERE id=?", partial.ID).Scan(&expiry); err != nil || expiry != claimExpiry {
			t.Fatal("existing claim ID/expiry changed", expiry, err)
		}
		detail, err := s.GetDeviceEvidenceDetail(ctx, storage.DeviceEvidenceDetailQuery{ScopeID: o.ScopeID, DeviceID: plan.Result.DeviceID, AsOf: later})
		if err != nil || len(detail.Evidence) != 2 {
			t.Fatal(detail, err)
		}
		for _, e := range detail.Evidence {
			want := map[domain.ClaimKind]string{domain.ClaimMAC: "02:00:00:00:00:01", domain.ClaimIPv4: "192.168.50.20"}
			if value, ok := want[e.Kind]; !ok || e.Value != value {
				t.Fatal("original claim value changed", e)
			}
			if !e.ObservedAt.Equal(*o.SourceTime) || e.Observation == nil || e.Observation.ID != o.ID || e.Value == "192.168.50.99" || e.Value == "02:00:00:00:00:ff" {
				t.Fatal("retry fabricated identity", e)
			}
		}
	}
}

func TestLegacyRepairLateFailureRollsBackAndRetries(t *testing.T) {
	_, db, o := legacyRepairFixture(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_repair_link BEFORE INSERT ON device_claim_links BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if repaired, err := repairLegacy(t, db, o); err == nil || repaired {
		t.Fatal("failed repair succeeded", repaired, err)
	}
	legacyRowCount(t, db, "observations", 1)
	for _, table := range []string{"identity_claims", "device_claim_links", "devices", "evidence_batches"} {
		legacyRowCount(t, db, table, 0)
	}
	if _, err := db.Exec("DROP TRIGGER reject_repair_link"); err != nil {
		t.Fatal(err)
	}
	if repaired, err := repairLegacy(t, db, o); err != nil || !repaired {
		t.Fatal(repaired, err)
	}
	legacyRowCount(t, db, "identity_claims", 2)
	legacyRowCount(t, db, "device_claim_links", 2)
	legacyRowCount(t, db, "devices", 1)
}

func TestLegacyRepairRejectsExpiredMissingWrongScopeAndCorruptOriginal(t *testing.T) {
	for _, mode := range []string{"expired", "missing", "wrong-scope", "oversized-payload", "oversized-event", "invalid-neighbor"} {
		t.Run(mode, func(t *testing.T) {
			_, db, o := legacyRepairFixture(t)
			wantError := true
			switch mode {
			case "expired":
				if _, err := db.Exec("UPDATE observations SET expires_at_ns=1"); err != nil {
					t.Fatal(err)
				}
				wantError = false
			case "missing":
				o.SourceKey = "missing"
				wantError = false
			case "wrong-scope":
				o.ScopeID = "scope.other"
			case "oversized-payload":
				raw, _ := json.Marshal(map[string]string{"value": strings.Repeat("x", 65536)})
				if _, err := db.Exec("UPDATE observations SET payload=?", string(raw)); err != nil {
					t.Fatal(err)
				}
			case "oversized-event":
				if _, err := db.Exec("UPDATE observations SET source_event_id=?", strings.Repeat("x", 600)); err != nil {
					t.Fatal(err)
				}
			case "invalid-neighbor":
				if _, err := db.Exec(`UPDATE observations SET payload='"invalid-neighbor"'`); err != nil {
					t.Fatal(err)
				}
			}
			repaired, err := repairLegacy(t, db, o)
			if repaired || (err != nil) != wantError {
				t.Fatal(mode, repaired, err)
			}
			legacyRowCount(t, db, "identity_claims", 0)
			legacyRowCount(t, db, "devices", 0)
		})
	}
}
