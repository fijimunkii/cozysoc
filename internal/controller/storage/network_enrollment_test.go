package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestEnrollDeviceWatchScopeIsIdempotentAuditedAndConflictSafe(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Unix(1_800_000_000, 0).UTC()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	metadata := json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.1.0/24"]}}`)

	first, changed, err := store.EnrollDeviceWatchScope(ctx, metadata)
	if err != nil || !changed {
		t.Fatalf("first enrollment changed=%v err=%v", changed, err)
	}
	if first.ID == "" || first.Kind != "lan" || !first.EnrolledAt.Equal(now) {
		t.Fatalf("unexpected enrolled scope: %+v", first)
	}
	if got := countNetworkEnrollmentAudits(t, store); got != 1 {
		t.Fatalf("audit count = %d, want 1", got)
	}

	second, changed, err := store.EnrollDeviceWatchScope(ctx, json.RawMessage(`{ "device_watch": { "prefixes": ["192.168.1.0/24"], "interface_index": 7, "interface_name": "en0" } }`))
	if err != nil || changed || second.ID != first.ID {
		t.Fatalf("idempotent enrollment scope=%+v changed=%v err=%v", second, changed, err)
	}
	if got := countNetworkEnrollmentAudits(t, store); got != 1 {
		t.Fatalf("idempotent enrollment added audit row: %d", got)
	}

	_, changed, err = store.EnrollDeviceWatchScope(ctx, json.RawMessage(`{"device_watch":{"interface_name":"en1","interface_index":8,"prefixes":["10.0.0.0/24"]}}`))
	if !errors.Is(err, ErrActiveDeviceWatchScopeExists) || changed {
		t.Fatalf("different active enrollment changed=%v err=%v", changed, err)
	}
	active, err := store.ListActiveDeviceWatchScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != first.ID {
		t.Fatalf("active scopes changed after conflict: %+v", active)
	}
}

func TestEnrollDeviceWatchScopeSerializesConcurrentAuthorization(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	metadatas := []json.RawMessage{
		json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.1.0/24"]}}`),
		json.RawMessage(`{"device_watch":{"interface_name":"en1","interface_index":8,"prefixes":["10.0.0.0/24"]}}`),
	}
	type result struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, len(metadatas))
	for _, metadata := range metadatas {
		metadata := metadata
		go func() {
			<-start
			_, changed, err := store.EnrollDeviceWatchScope(ctx, metadata)
			results <- result{changed: changed, err: err}
		}()
	}
	close(start)

	successes := 0
	conflicts := 0
	for range metadatas {
		result := <-results
		switch {
		case result.err == nil && result.changed:
			successes++
		case errors.Is(result.err, ErrActiveDeviceWatchScopeExists) && !result.changed:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent enrollment result: %+v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent enrollment successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestEnrollDeviceWatchScopeRollsBackWhenAuditFails(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.conn.ExecContext(ctx, `CREATE TRIGGER reject_network_scope_audit
		BEFORE INSERT ON audit_events WHEN NEW.kind = 'network-scope-enroll'
		BEGIN SELECT RAISE(ABORT, 'fixture audit failure'); END`); err != nil {
		t.Fatal(err)
	}

	_, changed, err := store.EnrollDeviceWatchScope(ctx, json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.1.0/24"]}}`))
	if err == nil || changed {
		t.Fatalf("audit failure changed=%v err=%v", changed, err)
	}
	active, listErr := store.ListActiveDeviceWatchScopes(ctx)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(active) != 0 {
		t.Fatalf("scope committed without audit: %+v", active)
	}
}

func TestEnrollDeviceWatchScopeRejectsInvalidMetadata(t *testing.T) {
	store, err := Open(t.TempDir(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, metadata := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"device_watch":null}`),
		json.RawMessage(`{"device_watch":"en0"}`),
		json.RawMessage(`{"device_watch":{"interface_name":"","interface_index":7,"prefixes":["192.168.1.0/24"]}}`),
		json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":0,"prefixes":["192.168.1.0/24"]}}`),
		json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.1.42/24"]}}`),
		json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.1.0/24","192.168.1.0/24"]}}`),
		json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.1.0/24"],"command":"nope"}}`),
	} {
		if _, _, err := store.EnrollDeviceWatchScope(context.Background(), metadata); err == nil {
			t.Fatalf("invalid metadata was accepted: %s", metadata)
		}
	}
}

func countNetworkEnrollmentAudits(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_events WHERE kind = 'network-scope-enroll'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
