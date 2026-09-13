package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func fixtureBatchPlan(r EvidenceBatchRecord) EvidenceBatchPlan {
	p := EvidenceBatchPlan{NewDevice: &domain.Device{ID: r.Links[0].DeviceID, CreatedAt: r.Claims[0].Claim.ObservedAt}, Links: r.Links}
	for _, c := range r.Claims {
		p.Claims = append(p.Claims, c.Claim)
	}
	return p
}
func stageFixture(t *testing.T, db *sql.DB, stager *EvidenceBatchStager, o domain.Observation) (EvidenceBatchRecord, bool, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	record, inserted, err := stager.Stage(context.Background(), tx, o)
	if err != nil {
		return record, false, err
	}
	return record, inserted, tx.Commit()
}
func assertStageTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
		t.Fatal(table, count, want, err)
	}
}

func TestEvidenceBatchStagerOwnsExpiryAndPreservesObservation(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	confidence := 0.8
	r.Observation.Confidence = &confidence
	original := cloneBatchObservation(*r.Observation)
	limits := DefaultLimits()
	stager, err := NewEvidenceBatchStager(limits, func(_ context.Context, _ *MixedIdentitySnapshot, o domain.Observation) (EvidenceBatchPlan, error) {
		// A producer receives a private input, not authority to rewrite evidence.
		o.Payload[0] = 'x'
		*o.Confidence = 0.1
		*o.SourceTime = o.SourceTime.Add(time.Hour)
		return fixtureBatchPlan(r), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := r.Observation.IngestedAt
	calls := 0
	stager.now = func() time.Time { at := base.Add(time.Duration(calls)); calls++; return at }
	limits.Retention[domain.RetentionStandard] = time.Hour
	got, inserted, err := stageFixture(t, db, stager, *r.Observation)
	if err != nil || !inserted || calls != 4 {
		t.Fatal(inserted, err, calls)
	}
	if !reflect.DeepEqual(*r.Observation, original) || !reflect.DeepEqual(*got.Observation, original) {
		t.Fatal("planner mutated observation")
	}
	want := base.Add(30*24*time.Hour + time.Nanosecond)
	if !got.ObservationExpiresAt.Equal(want) || !got.Claims[0].ExpiresAt.Equal(want.Add(time.Nanosecond)) || !got.Claims[1].ExpiresAt.Equal(want.Add(2*time.Nanosecond)) {
		t.Fatal("expiry policy or independent clock changed", got)
	}
	reopened, err := batchSQLRead(t, db, original.ScopeID, original.ID, base)
	if err != nil || !reflect.DeepEqual(reopened, got) {
		t.Fatal("staged record differs from durable evidence", err)
	}
}

func TestEvidenceBatchStagerReplaySkipsPlannerAndClock(t *testing.T) {
	for _, format := range []string{"batch", "legacy"} {
		t.Run(format, func(t *testing.T) {
			s, db := batchSQLFixture(t)
			r := batchSQLRecord(1)
			if format == "batch" {
				// Keep encoded expiry and lookup metadata consistent for an expired,
				// unpruned observation rather than corrupting only the lookup column.
				expired := time.Now().UTC().Add(-time.Hour)
				r.ObservationExpiresAt = &expired
				if _, err := batchSQLAppend(t, db, r); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.InsertObservation(context.Background(), *r.Observation); err != nil {
					t.Fatal(err)
				}
			}
			stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
				t.Fatal("replay invoked planner")
				return EvidenceBatchPlan{}, nil
			})
			stager.now = func() time.Time { t.Fatal("replay renewed expiry"); return time.Time{} }
			replay := *batchSQLRecord(2).Observation
			replay.SourceKey = r.Observation.SourceKey
			got, inserted, err := stageFixture(t, db, stager, replay)
			expectedError := error(nil)
			if format == "legacy" {
				expectedError = ErrEvidenceBatchLegacyReplay
			}
			if !errors.Is(err, expectedError) || inserted || !reflect.DeepEqual(got, EvidenceBatchRecord{}) {
				t.Fatal("source-key replay", inserted, got, err)
			}
			assertStageTableCount(t, db, "devices", 0)
			want := 0
			if format == "batch" {
				want = 1
			}
			assertStageTableCount(t, db, "evidence_batch_lookup", want)
			// Suppression is storage-based, not dependent on whether evidence has expired.
			if format == "legacy" {
				if _, err := db.Exec("UPDATE observations SET expires_at_ns=1"); err != nil {
					t.Fatal(err)
				}
			}
			if _, inserted, err := stageFixture(t, db, stager, replay); !errors.Is(err, expectedError) || inserted {
				t.Fatal("unpruned replay was accepted", inserted, err)
			}
		})
	}
}

func TestEvidenceBatchStagerRejectsIDConflictAndWrongScopeBeforePlanning(t *testing.T) {
	for _, format := range []string{"batch", "legacy"} {
		t.Run(format, func(t *testing.T) {
			s, db := batchSQLFixture(t)
			r := batchSQLRecord(1)
			if format == "batch" {
				if _, err := batchSQLAppend(t, db, r); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.InsertObservation(context.Background(), *r.Observation); err != nil {
					t.Fatal(err)
				}
			}
			stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
				t.Fatal("invalid source reached planner")
				return EvidenceBatchPlan{}, nil
			})
			collision := *r.Observation
			collision.SourceKey = "another.key"
			if _, ok, err := stageFixture(t, db, stager, collision); err == nil || ok {
				t.Fatal("ID collision accepted", ok, err)
			}
			collision.ScopeID = "scope.other"
			if _, ok, err := stageFixture(t, db, stager, collision); err == nil || ok {
				t.Fatal("sensor authorized another scope", ok, err)
			}
		})
	}
}

func TestEvidenceBatchStagerRollsBackDeviceAndAllIndexes(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return fixtureBatchPlan(r), nil
	})
	if _, err := db.Exec(`CREATE TRIGGER fail_staged_lookup BEFORE INSERT ON evidence_batch_lookup BEGIN SELECT RAISE(ABORT,'injected lookup failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := stageFixture(t, db, stager, *r.Observation); err == nil || ok || !reflect.DeepEqual(got, EvidenceBatchRecord{}) {
		t.Fatal("failed write acknowledged", ok, err)
	}
	for _, table := range []string{"devices", "evidence_batch_sources", "evidence_batches", "evidence_batch_lookup", "evidence_batch_identity_groups", "evidence_batch_identity_routes"} {
		assertStageTableCount(t, db, table, 0)
	}
	if _, err := db.Exec("DROP TRIGGER fail_staged_lookup"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := stageFixture(t, db, stager, *r.Observation); err != nil || !ok {
		t.Fatal("retry after rollback", ok, err)
	}
	assertStageTableCount(t, db, "devices", 1)
	assertStageTableCount(t, db, "evidence_batch_lookup", 1)
}

func TestEvidenceBatchStagerLeavesCommitWithOwner(t *testing.T) {
	s, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		return fixtureBatchPlan(r), nil
	})
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, ok, err := stager.Stage(context.Background(), tx, *r.Observation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var count int
	if err := s.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM devices").Scan(&count); err != nil || count != 0 {
		t.Fatal("stager committed without owner", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertStageTableCount(t, db, "devices", 0)
	if _, ok, err := stager.Stage(context.Background(), tx, *r.Observation); !errors.Is(err, sql.ErrTxDone) || ok {
		t.Fatal("closed transaction escaped", ok, err)
	}
}

func TestEvidenceBatchStagerRejectsInvalidPlansAndMissingDevices(t *testing.T) {
	for _, mutation := range []string{"missing-device", "wrong-claim-source", "unused-device", "oversized", "planner-error"} {
		t.Run(mutation, func(t *testing.T) {
			_, db := batchSQLFixture(t)
			r := batchSQLRecord(1)
			sentinel := errors.New("planner failure")
			stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
				p := fixtureBatchPlan(r)
				switch mutation {
				case "missing-device":
					p.NewDevice = nil
				case "wrong-claim-source":
					p.Claims[0].ScopeID = "scope.other"
				case "unused-device":
					p.NewDevice.ID = "device.unused"
				case "oversized":
					for len(p.Claims) <= EvidenceBatchMaxAssociations {
						p.Claims = append(p.Claims, p.Claims[0])
					}
				case "planner-error":
					return p, sentinel
				}
				return p, nil
			})
			if got, ok, err := stageFixture(t, db, stager, *r.Observation); err == nil || ok || !reflect.DeepEqual(got, EvidenceBatchRecord{}) {
				t.Fatal("invalid plan accepted", ok, err)
			}
			assertStageTableCount(t, db, "devices", 0)
			assertStageTableCount(t, db, "evidence_batch_lookup", 0)
		})
	}
}

func TestEvidenceBatchStagerKeepsAmbiguousClaimsWithoutDevice(t *testing.T) {
	_, db := batchSQLFixture(t)
	r := batchSQLRecord(1)
	stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		p := fixtureBatchPlan(r)
		p.NewDevice = nil
		p.Links = nil
		return p, nil
	})
	got, ok, err := stageFixture(t, db, stager, *r.Observation)
	if err != nil || !ok || len(got.Claims) != 2 || len(got.Links) != 0 {
		t.Fatal("ambiguous evidence lost", ok, err)
	}
	assertStageTableCount(t, db, "devices", 0)
	assertStageTableCount(t, db, "evidence_batch_lookup", 1)
}

func TestEvidenceBatchStagerRequiresReservedSchemaEvenForLegacyReplay(t *testing.T) {
	s, db, _ := legacySnapshotFixture(t)
	r := batchSQLRecord(1)
	r.Observation.ScopeID = "scope.home"
	r.Observation.SensorID = "sensor.home"
	if _, err := s.InsertObservation(context.Background(), *r.Observation); err != nil {
		t.Fatal(err)
	}
	stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		t.Fatal("missing schema reached planner")
		return EvidenceBatchPlan{}, nil
	})
	if _, ok, err := stageFixture(t, db, stager, *r.Observation); err == nil || ok {
		t.Fatal("missing schema silently fell back", ok, err)
	}
}

func TestEvidenceBatchStagerAbsentSourceDoesNotAliasSourceZero(t *testing.T) {
	s, db := batchSQLFixture(t)
	other := batchSQLRecord(1)
	seedScopeAndSensor(t, s, "scope.other", "sensor.other", other.Observation.IngestedAt)
	other.Observation.ScopeID = "scope.other"
	other.Observation.SensorID = "sensor.other"
	for i := range other.Claims {
		other.Claims[i].Claim.ScopeID = "scope.other"
		other.Claims[i].Claim.SourceSensorID = "sensor.other"
	}
	if _, err := db.Exec(`INSERT INTO evidence_batch_sources(id,scope_id,sensor_id,stream) VALUES(0,'scope.other','sensor.other',?)`, other.Observation.SourceStream); err != nil {
		t.Fatal(err)
	}
	if _, err := batchSQLAppend(t, db, other); err != nil {
		t.Fatal(err)
	}
	current := batchSQLRecord(2)
	current.Observation.SourceKey = other.Observation.SourceKey
	calls := 0
	stager, _ := NewEvidenceBatchStager(DefaultLimits(), func(context.Context, *MixedIdentitySnapshot, domain.Observation) (EvidenceBatchPlan, error) {
		calls++
		return fixtureBatchPlan(current), nil
	})
	if _, ok, err := stageFixture(t, db, stager, *current.Observation); err != nil || !ok || calls != 1 {
		t.Fatal("missing source aliased unrelated source zero", ok, err, calls)
	}
}
