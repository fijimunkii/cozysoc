package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func TestSetDeviceLabelIsScopedIdempotentAndAudited(t *testing.T) {
	store, scopeID, deviceID := newLabelFixture(t)
	ctx := context.Background()

	changed, err := store.SetDeviceLabel(ctx, scopeID, deviceID, "Living Room TV")
	if err != nil || !changed {
		t.Fatalf("set label changed=%v err=%v", changed, err)
	}
	if got := readDeviceLabel(t, store, deviceID); got != "Living Room TV" {
		t.Fatalf("device label = %q", got)
	}
	if got := labelAuditCount(t, store, deviceID); got != 1 {
		t.Fatalf("audit count = %d, want 1", got)
	}

	changed, err = store.SetDeviceLabel(ctx, scopeID, deviceID, "Living Room TV")
	if err != nil || changed {
		t.Fatalf("idempotent set changed=%v err=%v", changed, err)
	}
	if got := labelAuditCount(t, store, deviceID); got != 1 {
		t.Fatalf("idempotent set added audit row: %d", got)
	}

	changed, err = store.SetDeviceLabel(ctx, scopeID, deviceID, "")
	if err != nil || !changed {
		t.Fatalf("clear label changed=%v err=%v", changed, err)
	}
	if got := readDeviceLabel(t, store, deviceID); got != "" {
		t.Fatalf("cleared device label = %q", got)
	}
	if got := labelAuditCount(t, store, deviceID); got != 2 {
		t.Fatalf("clear audit count = %d, want 2", got)
	}

	var actor, payload string
	if err := store.conn.QueryRowContext(ctx, `SELECT actor, payload FROM audit_events WHERE kind = 'device-label' ORDER BY occurred_at_ns DESC, id DESC LIMIT 1`).Scan(&actor, &payload); err != nil {
		t.Fatal(err)
	}
	if actor != "local-os-user" {
		t.Fatalf("audit actor = %q", actor)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(payload), &details); err != nil {
		t.Fatal(err)
	}
	if details["state"] != "applied" || details["scope_id"] != scopeID || details["device_id"] != deviceID {
		t.Fatalf("unexpected audit payload: %+v", details)
	}
}

func TestSetDeviceLabelFailsClosedOutsideScopeAndOnHostileLabels(t *testing.T) {
	store, scopeID, deviceID := newLabelFixture(t)
	ctx := context.Background()

	for _, test := range []struct {
		name  string
		scope string
		label string
	}{
		{name: "wrong scope", scope: "scope.other", label: "TV"},
		{name: "leading space", scope: scopeID, label: " TV"},
		{name: "control", scope: scopeID, label: "TV\nKitchen"},
		{name: "oversized", scope: scopeID, label: strings.Repeat("x", MaxDeviceLabelBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed, err := store.SetDeviceLabel(ctx, test.scope, deviceID, test.label)
			if err == nil || changed {
				t.Fatalf("mutation changed=%v err=%v", changed, err)
			}
			if test.name == "wrong scope" && !errors.Is(err, ErrDeviceNotInScope) {
				t.Fatalf("wrong-scope error = %v", err)
			}
		})
	}
	if got := readDeviceLabel(t, store, deviceID); got != "" {
		t.Fatalf("rejected mutation changed label to %q", got)
	}
	if got := labelAuditCount(t, store, deviceID); got != 0 {
		t.Fatalf("rejected mutations wrote %d audit rows", got)
	}
}

func TestSetDeviceLabelRollsBackWhenAuditCannotCommit(t *testing.T) {
	store, scopeID, deviceID := newLabelFixture(t)
	ctx := context.Background()
	if _, err := store.conn.ExecContext(ctx, `CREATE TRIGGER reject_device_label_audit
		BEFORE INSERT ON audit_events WHEN NEW.kind = 'device-label'
		BEGIN SELECT RAISE(ABORT, 'fixture audit failure'); END`); err != nil {
		t.Fatal(err)
	}

	changed, err := store.SetDeviceLabel(ctx, scopeID, deviceID, "Kitchen Speaker")
	if err == nil || changed {
		t.Fatalf("audit failure changed=%v err=%v", changed, err)
	}
	if got := readDeviceLabel(t, store, deviceID); got != "" {
		t.Fatalf("label committed without audit: %q", got)
	}
	if got := labelAuditCount(t, store, deviceID); got != 0 {
		t.Fatalf("audit failure left %d audit rows", got)
	}
}

func newLabelFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return now }

	scopeID := "scope.home"
	deviceID := "device.fixture"
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{ID: scopeID, Kind: "lan", EnrolledAt: now, Metadata: json.RawMessage(`{"fixture":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNetworkScope(ctx, domain.NetworkScope{ID: "scope.other", Kind: "lan", EnrolledAt: now, Metadata: json.RawMessage(`{"fixture":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSensor(ctx, domain.Sensor{ID: "sensor.fixture", ScopeID: scopeID, Kind: "fixture", Ownership: "builtin", RegisteredAt: now, Metadata: json.RawMessage(`{"fixture":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDevice(ctx, domain.Device{ID: deviceID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	confidence := 0.9
	validUntil := now.Add(10 * time.Minute)
	if err := store.InsertIdentityClaim(ctx, domain.IdentityClaim{
		ID: "claim.fixture", ScopeID: scopeID, Kind: domain.ClaimMAC, Value: "02:00:00:00:00:01",
		ObservedAt: now, ValidUntil: &validUntil, Confidence: &confidence, SourceSensorID: "sensor.fixture",
		Retention: domain.RetentionStandard,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDeviceClaimLink(ctx, domain.DeviceClaimLink{
		ID: "link.fixture", DeviceID: deviceID, ClaimID: "claim.fixture", ValidFrom: now, ValidUntil: &validUntil,
		Confidence: &confidence, Authority: domain.LinkInferred, Reason: "fixture", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return store, scopeID, deviceID
}

func readDeviceLabel(t *testing.T, store *Store, deviceID string) string {
	t.Helper()
	var label sql.NullString
	if err := store.conn.QueryRowContext(context.Background(), `SELECT user_label FROM devices WHERE id = ?`, deviceID).Scan(&label); err != nil {
		t.Fatal(err)
	}
	if !label.Valid {
		return ""
	}
	return label.String
}

func labelAuditCount(t *testing.T, store *Store, deviceID string) int {
	t.Helper()
	var count int
	if err := store.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_events WHERE kind = 'device-label' AND json_extract(payload, '$.device_id') = ?`, deviceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
