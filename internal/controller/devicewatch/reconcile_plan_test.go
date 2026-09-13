package devicewatch

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type planIdentityReader func(context.Context, string, domain.ClaimKind, string, time.Time, time.Time) ([]domain.Device, error)

func (f planIdentityReader) FindRecentDevicesByClaim(ctx context.Context, scope string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
	return f(ctx, scope, kind, value, since, until)
}

func TestReconciliationPlanReadOnlyDecisions(t *testing.T) {
	at := time.Date(2026, 9, 12, 12, 0, 0, 123, time.UTC)
	for _, tc := range []struct {
		name         string
		candidates   []domain.Device
		address, mac string
		sourceTime   bool
		kind         domain.ClaimKind
		confidence   float64
	}{
		{"new-ipv4", nil, "192.168.1.20", "02:00:00:00:00:01", true, domain.ClaimIPv4, .75},
		{"continuity-ipv6", []domain.Device{{ID: "device.existing", CreatedAt: at.Add(-time.Hour)}}, "fe80::20%en0", "00:11:22:33:44:55", false, domain.ClaimIPv6, .90},
		{"ambiguous", []domain.Device{{ID: "device.one"}, {ID: "device.two"}}, "192.168.1.21", "02:00:00:00:00:01", true, domain.ClaimIPv4, .75},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := neighborObservationFixture(t, "scope.home", "sensor.fixture", "obs.plan", tc.address, tc.mac, at)
			if !tc.sourceTime {
				observation.SourceTime = nil
			} else {
				// Source evidence wins over a later ingestion clock.
				observation.IngestedAt = at.Add(time.Hour)
			}
			calls := 0
			reader := planIdentityReader(func(ctx context.Context, scope string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
				calls++
				if scope != observation.ScopeID || kind != domain.ClaimMAC || value != tc.mac || !since.Equal(at.Add(-7*24*time.Hour)) || !until.Equal(at) {
					t.Fatal("changed continuity query", scope, kind, value, since, until)
				}
				return tc.candidates, nil
			})
			// This reader has no write methods. Planning must work without a writer.
			plan, err := PlanReconciliation(context.Background(), reader, observation)
			if err != nil || calls != 1 || len(plan.Claims) != 2 || plan.Result.Claims != 2 {
				t.Fatal(plan, err, calls)
			}
			for _, claim := range plan.Claims {
				if claim.SourceObservationID != observation.ID || claim.ScopeID != observation.ScopeID || claim.SourceSensorID != observation.SensorID || !claim.ObservedAt.Equal(at) || claim.ValidUntil == nil || !claim.ValidUntil.Equal(at.Add(10*time.Minute)) || claim.Retention != domain.RetentionStandard {
					t.Fatal("changed claim provenance", claim)
				}
			}
			if plan.Claims[0].Kind != domain.ClaimMAC || *plan.Claims[0].Confidence != tc.confidence || plan.Claims[1].Kind != tc.kind || *plan.Claims[1].Confidence != .65 {
				t.Fatal("claim kinds/confidence", plan.Claims)
			}
			if tc.kind == domain.ClaimIPv6 && plan.Claims[1].Value != "fe80::20" {
				t.Fatal("IPv6 zone retained")
			}
			if len(tc.candidates) > 1 {
				if !plan.Result.Ambiguous || plan.NewDevice != nil || len(plan.Links) != 0 || plan.Result.Created || plan.Result.DeviceID != "" {
					t.Fatal("ambiguity collapsed", plan)
				}
			} else {
				if plan.Result.Ambiguous || len(plan.Links) != 2 || plan.Result.DeviceID == "" || plan.Result.Created != (len(tc.candidates) == 0) {
					t.Fatal("identity decision", plan)
				}
				if len(tc.candidates) == 0 {
					if plan.NewDevice == nil || plan.NewDevice.ID != plan.Result.DeviceID || !plan.NewDevice.CreatedAt.Equal(at) {
						t.Fatal("new device evidence", plan)
					}
				} else if plan.NewDevice != nil || plan.Result.DeviceID != tc.candidates[0].ID {
					t.Fatal("continuity changed", plan)
				}
				for i, link := range plan.Links {
					if link.ClaimID != plan.Claims[i].ID || link.ID != "link.dw."+stableDigest("link-v1", plan.Result.DeviceID, link.ClaimID) || link.EvidenceObservationID != observation.ID || !link.ValidFrom.Equal(at) || !link.CreatedAt.Equal(at) || link.Authority != domain.LinkInferred {
						t.Fatal("changed link", link)
					}
				}
			}
			expiry := at.Add(30 * 24 * time.Hour)
			record := storage.EvidenceBatchRecord{Observation: &observation, ObservationExpiresAt: &expiry, Links: plan.Links}
			for _, claim := range plan.Claims {
				record.Claims = append(record.Claims, storage.RetainedIdentityClaim{Claim: claim, ExpiresAt: expiry})
			}
			encoded, err := storage.EncodeEvidenceBatch([]storage.EvidenceBatchRecord{record})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := storage.DecodeEvidenceBatch(encoded)
			if err != nil || !reflect.DeepEqual(decoded, []storage.EvidenceBatchRecord{record}) {
				t.Fatal("plan cannot retain exact evidence", err)
			}
		})
	}
}

func TestReconciliationPlanFailureReturnsNoPartialDecision(t *testing.T) {
	at := time.Now().UTC()
	observation := neighborObservationFixture(t, "scope.home", "sensor.fixture", "obs.plan", "192.168.1.20", "02:00:00:00:00:01", at)
	sentinel := errors.New("snapshot failed")
	calls := 0
	reader := planIdentityReader(func(context.Context, string, domain.ClaimKind, string, time.Time, time.Time) ([]domain.Device, error) {
		calls++
		return nil, sentinel
	})
	plan, err := PlanReconciliation(context.Background(), reader, observation)
	if !errors.Is(err, sentinel) || !reflect.DeepEqual(plan, ReconciliationPlan{}) || calls != 1 {
		t.Fatal(plan, err, calls)
	}
	observation.Kind = "unsupported"
	plan, err = PlanReconciliation(context.Background(), reader, observation)
	if err == nil || !reflect.DeepEqual(plan, ReconciliationPlan{}) || calls != 1 {
		t.Fatal("invalid input reached reader", plan, err, calls)
	}
	if _, err := PlanReconciliation(context.Background(), nil, observation); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestPlannedLegacyReconciliationKeepsExistingClaimIDs(t *testing.T) {
	store, scope, sensor := newIdentityFixtureStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	observation := neighborObservationFixture(t, scope, sensor, "obs.plan.replay", "192.168.1.20", "02:00:00:00:00:01", at)
	if _, err := store.InsertObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanReconciliation(ctx, store, observation)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a prior partial write with supported noncanonical claim IDs.
	for i, claim := range plan.Claims {
		claim.ID = []string{"claim.original.mac", "claim.original.ip"}[i]
		if _, err := store.EnsureIdentityClaim(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	reconciler, _ := NewReconciler(store)
	result, err := reconciler.ReconcileObservation(ctx, observation)
	if err != nil || result.DeviceID != plan.Result.DeviceID || !result.Created {
		t.Fatal(result, err)
	}
	detail, err := store.GetDeviceEvidenceDetail(ctx, storage.DeviceEvidenceDetailQuery{ScopeID: scope, DeviceID: result.DeviceID, AsOf: at})
	if err != nil || len(detail.Evidence) != 2 {
		t.Fatal("links did not use existing claim IDs", detail, err)
	}
	replay, err := reconciler.ReconcileObservation(ctx, observation)
	if err != nil || replay.Created || replay.DeviceID != result.DeviceID {
		t.Fatal("replay changed identity", replay, err)
	}
	detail, err = store.GetDeviceEvidenceDetail(ctx, storage.DeviceEvidenceDetailQuery{ScopeID: scope, DeviceID: result.DeviceID, AsOf: at})
	if err != nil || len(detail.Evidence) != 2 {
		t.Fatal("replay duplicated links", detail, err)
	}
}

func TestReconciliationPlanUsesCallerIdentityTransaction(t *testing.T) {
	store, scope, sensor := newIdentityFixtureStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	first := neighborObservationFixture(t, scope, sensor, "obs.snapshot.first", "192.168.1.20", "02:00:00:00:00:01", at.Add(-time.Hour))
	if _, err := store.InsertObservation(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconciler, _ := NewReconciler(store)
	original, err := reconciler.ReconcileObservation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	uri := url.URL{Scheme: "file", Path: store.Path()}
	uri.RawQuery = url.Values{"mode": {"rw"}, "_pragma": {"foreign_keys(1)", "synchronous(FULL)", "busy_timeout(100)"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader, err := storage.NewLegacyIdentitySnapshot(tx, at)
	if err != nil {
		t.Fatal(err)
	}
	current := neighborObservationFixture(t, scope, sensor, "obs.snapshot.current", "192.168.1.21", "02:00:00:00:00:01", at)
	plan, err := PlanReconciliation(ctx, reader, current)
	if err != nil || plan.Result.DeviceID != original.DeviceID || plan.NewDevice != nil {
		t.Fatal("snapshot lost original continuity", plan, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE devices SET retired_at_ns=? WHERE id=?", at.Add(-time.Nanosecond).UnixNano(), original.DeviceID); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanReconciliation(ctx, reader, current)
	if err != nil || plan.NewDevice == nil || plan.Result.DeviceID == original.DeviceID {
		t.Fatal("planner ignored staged retirement", plan, err)
	}
	outside, err := PlanReconciliation(ctx, store, current)
	if err != nil || outside.Result.DeviceID != original.DeviceID || outside.NewDevice != nil {
		t.Fatal("staged retirement escaped transaction", outside, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if plan, err := PlanReconciliation(ctx, reader, current); !errors.Is(err, sql.ErrTxDone) || !reflect.DeepEqual(plan, ReconciliationPlan{}) {
		t.Fatal("planner escaped closed snapshot", plan, err)
	}
	outside, err = PlanReconciliation(ctx, store, current)
	if err != nil || outside.Result.DeviceID != original.DeviceID {
		t.Fatal("rollback changed continuity", outside, err)
	}
}
