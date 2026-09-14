package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestBatchClaimSourceKeyMatchesLegacyNormalizedUniqueness(t *testing.T) {
	for _, value := range []string{"02:00:00:00:00:01", "02-00-00-00-00-01"} {
		t.Run(value, func(t *testing.T) {
			legacy, _ := detailBatchFixture(t)
			_, db := detailBatchFixture(t)
			r := detailRecord(1, 0)
			r.Claims[1].Claim.Kind = domain.ClaimMAC
			r.Claims[1].Claim.Value = value
			if _, err := legacy.InsertObservation(context.Background(), *r.Observation); err != nil {
				t.Fatal(err)
			}
			if err := legacy.InsertIdentityClaim(context.Background(), r.Claims[0].Claim); err != nil {
				t.Fatal(err)
			}
			if err := legacy.InsertIdentityClaim(context.Background(), r.Claims[1].Claim); err == nil {
				t.Fatal("legacy accepted duplicate normalized source key")
			}
			if ok, err := batchSQLAppend(t, db, r); !errors.Is(err, ErrEvidenceBatchData) || ok {
				t.Fatal(ok, err)
			}
			assertStageTableCount(t, db, "evidence_batches", 0)
			assertStageTableCount(t, db, "evidence_batch_sources", 0)
			// NULL source observations have different legacy UNIQUE semantics. Preserve
			// valid independently retained claims instead of deleting one during pruning.
			r.Observation = nil
			r.ObservationExpiresAt = nil
			for j := range r.Claims {
				r.Claims[j].Claim.SourceObservationID = ""
				r.Links[j].EvidenceObservationID = ""
			}
			for _, c := range r.Claims {
				claim := c.Claim
				claim.ID += ".null"
				if err := legacy.InsertIdentityClaim(context.Background(), claim); err != nil {
					t.Fatal("legacy NULL source must permit repeated values", err)
				}
			}
			if err := validateRetainedBatchBundle(r, "scope.fixture", "sensor.fixture", "fixture"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBatchRejectsLegacyIdentityIDsAndRollsBackPlannerDevice(t *testing.T) {
	for _, mode := range []string{"claim", "link", "expired-claim"} {
		t.Run(mode, func(t *testing.T) {
			s, db := detailBatchFixture(t)
			old := detailRecord(1, 0)
			storeLegacyDetailRecord(t, s, old)
			r := deviceListRecord(2, "device.new", 0)
			switch mode {
			case "claim", "expired-claim":
				r.Claims[0].Claim.ID = old.Claims[0].Claim.ID
				r.Links[0].ClaimID = r.Claims[0].Claim.ID
			case "link":
				r.Links[0].ID = old.Links[0].ID
			}
			if mode == "expired-claim" {
				if _, err := db.Exec("UPDATE identity_claims SET expires_at_ns=?", old.Claims[0].Claim.ObservedAt.Add(-time.Hour).UnixNano()); err != nil {
					t.Fatal(err)
				}
			}
			stager, err := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
				return fixtureBatchPlan(r), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got, ok, err := stageFixture(t, db, stager, *r.Observation)
			if !errors.Is(err, ErrEvidenceBatchData) || ok || !reflect.DeepEqual(got, EvidenceBatchRecord{}) {
				t.Fatal(got, ok, err)
			}
			for _, table := range []string{"evidence_batches", "evidence_batch_sources", "evidence_batch_lookup", "evidence_batch_identity_groups"} {
				assertStageTableCount(t, db, table, 0)
			}
			assertStageTableCount(t, db, "devices", 1)
			assertStageTableCount(t, db, "identity_claims", 2)
			assertStageTableCount(t, db, "device_claim_links", 2)
			// A retry with fresh IDs succeeds after the rejected transaction rolled back.
			r = deviceListRecord(2, "device.new", 0)
			if _, ok, err := stageFixture(t, db, stager, *r.Observation); err != nil || !ok {
				t.Fatal(ok, err)
			}
			assertStageTableCount(t, db, "devices", 2)
		})
	}
}

func TestRetainedBatchReadAndPruneRejectDuplicateSourceKey(t *testing.T) {
	_, db := detailBatchFixture(t)
	r := detailRecord(1, 0)
	if ok, err := batchSQLAppend(t, db, r); err != nil || !ok {
		t.Fatal(ok, err)
	}
	r.Claims[1].Claim.Kind = domain.ClaimMAC
	r.Claims[1].Claim.Value = r.Claims[0].Claim.Value
	// Codec preserves domain-valid input; the SQL adapter supplies relational
	// constraints, including when stored payload bytes have been corrupted.
	data, err := EncodeEvidenceBatch([]EvidenceBatchRecord{r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE evidence_batches SET data=?", data); err != nil {
		t.Fatal(err)
	}
	if _, err := batchSQLRead(t, db, r.Observation.ScopeID, r.Observation.ID, r.Observation.IngestedAt); err == nil {
		t.Fatal("read accepted duplicate claim key")
	}
	if _, err := batchSQLPrune(t, db, *r.ObservationExpiresAt, 1); err == nil {
		t.Fatal("prune silently repaired duplicate key")
	}
	var retained []byte
	if err := db.QueryRow("SELECT data FROM evidence_batches").Scan(&retained); err != nil || !reflect.DeepEqual(retained, data) {
		t.Fatal("failed prune changed payload", err)
	}
}
