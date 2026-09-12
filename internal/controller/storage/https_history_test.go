package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func httpsHistoryStore(t *testing.T) (*Store, HTTPSHistoryQuery, []httpsrun.Event) {
	t.Helper()
	store, _ := gatewayAuditStore(t)
	return seedHTTPSHistoryStore(t, store)
}

func seedHTTPSHistoryStore(t *testing.T, store *Store) (*Store, HTTPSHistoryQuery, []httpsrun.Event) {
	t.Helper()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	scope := domain.NetworkScope{ID: "scope.history", Kind: "lan", EnrolledAt: now.Add(-7 * 24 * time.Hour), Metadata: json.RawMessage(`{"device_watch":{"interface_name":"en0","interface_index":7,"prefixes":["192.168.50.0/24"]}}`)}
	if err := store.CreateNetworkScope(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-10 * time.Second)
	base := httpsrun.Event{SchemaVersion: 1, RunID: strings.Repeat("a", 32), Profile: httpsrun.Profile, Selection: nq.HTTPSSelection{ID: "selection-v1", EndpointID: "endpoint-v1", RequestID: "request-v1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Observer: nq.Observer{ScopeID: scope.ID, SensorID: "local", InterfaceName: "en0", InterfaceIndex: 7}}
	auth := base
	auth.State, auth.At = "authorized", at
	admit := base
	admit.State, admit.At = "admitted", at
	end := at.Add(3 * time.Second)
	terminal := base
	terminal.State, terminal.Outcome, terminal.At = "finished", "completed", end
	terminal.Measurement = &httpsrun.Measurement{StartedAt: at, CompletedAt: end, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSTimeout, Stage: nq.HTTPSRequest}
	return store, HTTPSHistoryQuery{ScopeID: scope.ID, AsOf: now}, []httpsrun.Event{auth, admit, terminal}
}

func insertHTTPSHistory(t *testing.T, s *Store, events []httpsrun.Event) {
	t.Helper()
	for _, e := range events {
		if err := s.InsertHTTPSRunAudit(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPSHistoryReadAndExactReferenceAreSideEffectFree(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	insertHTTPSHistory(t, s, e)
	ctx := context.Background()
	for _, id := range []string{"", e[0].RunID} {
		q.RunID = id
		page, err := s.ReadHTTPSHistory(ctx, q)
		if err != nil || len(page.Runs) != 1 || page.Runs[0].Outcome != "completed" || page.Runs[0].Assessment.State != "timeout" || page.Runs[0].Measurement.ResponseTimeNanoseconds != nil || !page.Runs[0].TerminalRetained {
			t.Fatalf("%+v %v", page, err)
		}
		if page.Truncated || page.ScanTruncated || httpsAuditCount(t, s) != 3 {
			t.Fatal("read changed audits or completeness")
		}
	}
	q.RunID = strings.Repeat("f", 32)
	if _, err := s.ReadHTTPSHistory(ctx, q); err != ErrHTTPSHistoryNotFound {
		t.Fatal("missing reference not hidden", err)
	}
	q.RunID = e[0].RunID
	q.ScopeID = "scope.other"
	if _, err := s.ReadHTTPSHistory(ctx, q); err != ErrHTTPSHistoryNotFound {
		t.Fatal("scope override exposed history", err)
	}
	q.ScopeID = e[0].Observer.ScopeID
	if _, err := s.conn.ExecContext(ctx, `UPDATE network_scopes SET retired_at_ns=? WHERE id=?`, q.AsOf.UnixNano(), q.ScopeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadHTTPSHistory(ctx, q); err != ErrHTTPSHistoryNotFound {
		t.Fatal("retired scope exposed history", err)
	}
}

func TestHTTPSHistoryRetentionMissingPhases(t *testing.T) {
	for _, mode := range []string{"missing-terminal", "expired-terminal", "expired-ancestors"} {
		t.Run(mode, func(t *testing.T) {
			s, q, e := httpsHistoryStore(t)
			ctx := context.Background()
			if mode == "missing-terminal" {
				e = e[:2]
			}
			insertHTTPSHistory(t, s, e)
			if mode == "expired-terminal" || mode == "expired-ancestors" {
				operator := "="
				if mode == "expired-ancestors" {
					operator = "!="
				}
				_, err := s.conn.ExecContext(ctx, `UPDATE audit_events SET expires_at_ns=? WHERE json_extract(payload,'$.state') `+operator+` 'finished'`, q.AsOf.UnixNano())
				if err != nil {
					t.Fatal(err)
				}
			}
			page, err := s.ReadHTTPSHistory(ctx, q)
			if err != nil || len(page.Runs) != 1 {
				t.Fatalf("%+v %v", page, err)
			}
			r := page.Runs[0]
			switch mode {
			case "missing-terminal", "expired-terminal":
				if r.Outcome != "unknown" || r.TerminalRetained || r.Measurement != nil {
					t.Fatal("absent terminal became a result")
				}
			case "expired-ancestors":
				if r.AuthorizationRetained || r.AdmissionRetained || !r.TerminalRetained || r.Measurement == nil {
					t.Fatal("expired phases resurrected")
				}

			}
		})
	}
}

func TestHTTPSExactHistoryCanReadOlderRetainedRunsButNotExpiredOnes(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	for i := range e {
		e[i].At = e[i].At.Add(-48 * time.Hour)
	}
	e[2].Measurement.StartedAt = e[2].Measurement.StartedAt.Add(-48 * time.Hour)
	end := e[2].Measurement.CompletedAt.Add(-48 * time.Hour)
	e[2].Measurement.CompletedAt = end
	insertHTTPSHistory(t, s, e)
	page, err := s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || len(page.Runs) != 0 {
		t.Fatal("recent window expanded", err)
	}
	q.RunID = e[0].RunID
	page, err = s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || len(page.Runs) != 1 {
		t.Fatal("retained old reference lost", err)
	}
	if _, err := s.conn.ExecContext(context.Background(), `UPDATE audit_events SET expires_at_ns=?`, q.AsOf.UnixNano()); err != nil {
		t.Fatal(err)
	}
	// Backdating the observation time cannot resurrect expired rows.
	q.AsOf = q.AsOf.Add(-time.Hour)
	if _, err := s.ReadHTTPSHistory(context.Background(), q); err != ErrHTTPSHistoryNotFound {
		t.Fatal("expired evidence resurrected", err)
	}
	if httpsAuditCount(t, s) != 3 {
		t.Fatal("read pruned expired evidence")
	}
}

func TestHTTPSHistoryBoundsAndExactLookupEscapeListTruncation(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	insertHTTPSHistory(t, s, e)
	for i := 0; i < MaxHTTPSHistoryScan+1; i++ {
		if err := s.InsertAuditEvent(context.Background(), domain.AuditEvent{ID: fmt.Sprintf("audit.other.%04d", i), Kind: "other", Actor: "controller", OccurredAt: q.AsOf, SchemaVersion: 1, Payload: json.RawMessage(`{}`), Retention: domain.RetentionAudit}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || !page.ScanTruncated || len(page.Runs) != 0 {
		t.Fatal("scan truncation hidden", err)
	}
	q.RunID = e[0].RunID
	page, err = s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || len(page.Runs) != 1 || page.ScanTruncated {
		t.Fatal("exact keys scanned unrelated audits", err)
	}
}

func TestHTTPSHistoryRunLimitAndConsistentPhaseLookup(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	for i := 0; i < MaxHTTPSHistoryRuns+1; i++ {
		copy := e[0]
		copy.RunID = fmt.Sprintf("%032x", i)
		insertHTTPSHistory(t, s, []httpsrun.Event{copy})
	}
	page, err := s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || !page.Truncated || len(page.Runs) != MaxHTTPSHistoryRuns {
		t.Fatal("run limit hidden", err)
	}
	for i, r := range page.Runs {
		if r.RunID != fmt.Sprintf("%032x", i) || r.Outcome != "unknown" {
			t.Fatal("unstable ordering or invented outcome")
		}
	}
	// The indexed scan can split a run's three phases; exact sibling reads must
	// still find a retained terminal or authorization outside the scan window.
}

func TestCorruptHTTPSAuditsNeverReturnPartialSuccess(t *testing.T) {
	for name, update := range map[string]string{
		"version":            `schema_version=99`,
		"actor":              `actor='other'`,
		"envelope-time":      `occurred_at_ns=occurred_at_ns+1`,
		"scope-conflict":     `payload=json_set(payload,'$.observer.ScopeID','scope.other')`,
		"selection-conflict": `payload=json_set(payload,'$.selection.RequestID','cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc')`,
		"oversized-actor":    `actor=printf('%05000d',0)`,
		"oversized-id":       `id=printf('%05000d',0)`,
		"oversized":          `payload=json_set(payload,'$.padding',printf('%05000d',0))`,
		"unknown-field":      `payload=json_set(payload,'$.secret','do-not-echo')`,
		"impossible-counts":  `payload=json_set(payload,'$.measurement.reply.RCode',50)`,
	} {
		t.Run(name, func(t *testing.T) {
			s, q, e := httpsHistoryStore(t)
			insertHTTPSHistory(t, s, e)
			if _, err := s.conn.ExecContext(context.Background(), `UPDATE audit_events SET `+update+` WHERE id=?`, "audit.https-run."+e[0].RunID+".finished"); err != nil {
				t.Fatal(err)
			}
			page, err := s.ReadHTTPSHistory(context.Background(), q)
			if err == nil || !reflect.DeepEqual(page, HTTPSHistoryPage{}) || strings.Contains(err.Error(), "do-not-echo") {
				t.Fatalf("corrupt evidence exposed: %+v %v", page, err)
			}
		})
	}
}

func TestHTTPSHistoryRejectsInvalidQueriesAndCancellation(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	insertHTTPSHistory(t, s, e)
	for _, id := range []string{"SQL'", "A" + strings.Repeat("a", 31), strings.Repeat("a", 33)} {
		bad := q
		bad.RunID = id
		if _, err := s.ReadHTTPSHistory(context.Background(), bad); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadHTTPSHistory(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestHTTPSHistorySurvivesReopenAndWindowBoundary(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	// Authorization just outside the recent-list window, terminal just inside.
	delta := q.AsOf.Add(-HTTPSHistoryWindow).Sub(e[2].At)
	for i := range e {
		e[i].At = e[i].At.Add(delta)
	}
	e[2].Measurement.StartedAt = e[2].Measurement.StartedAt.Add(delta)
	end := e[2].Measurement.CompletedAt.Add(delta)
	e[2].Measurement.CompletedAt = end
	insertHTTPSHistory(t, s, e)
	before, err := s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || len(before.Runs) != 1 || !before.Runs[0].AuthorizationRetained {
		t.Fatal("window boundary split retained lifecycle", err)
	}
	var path string
	if err := s.conn.QueryRowContext(context.Background(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Dir(path), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.now = func() time.Time { return q.AsOf }
	after, err := reopened.ReadHTTPSHistory(context.Background(), q)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart changed retained evidence", err)
	}
}

func TestHTTPSHistoryReadTransactionCannotAbsorbOrUndoAuditWrites(t *testing.T) {
	s, q, events := httpsHistoryStore(t)
	insertHTTPSHistory(t, s, events[:2])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_events`).Scan(&count); err != nil || count != 2 {
		t.Fatal("read snapshot unavailable", err)
	}
	// SQLite mode=ro, not merely a caller convention or ignored transaction hint.
	if _, err := tx.ExecContext(ctx, `UPDATE audit_events SET actor='forbidden'`); err == nil {
		t.Fatal("history connection can write")
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- s.InsertHTTPSRunAudit(ctx, events[2])
	}()
	<-started
	// A same-connection audit can report success within somebody else's read
	// transaction. A separate rollback-journal reader cannot acknowledge that
	// commit until this snapshot's lock is released.
	select {
	case err := <-done:
		t.Fatalf("writer returned before read snapshot closed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_events`).Scan(&count); err != nil || count != 2 {
		t.Fatal("snapshot changed under concurrent writer", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("audit commit failed after read released", err)
	}
	page, err := s.ReadHTTPSHistory(ctx, q)
	if err != nil || len(page.Runs) != 1 || !page.Runs[0].TerminalRetained || httpsAuditCount(t, s) != 3 {
		t.Fatal("read rollback lost an acknowledged terminal audit", err)
	}
}

func TestHTTPSHistoryReadPoolIsBoundedAndUsesLiteralPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history ?#%literal")
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, q, _ := seedHTTPSHistoryStore(t, s)
	reader := s.gatewayHistoryDB
	ctx := context.Background()
	conn, err := reader.Conn(ctx)
	if err != nil {
		t.Fatal("literal path was interpreted as query syntax", err)
	}
	defer conn.Close()
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM audit_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal(err)
	}
	limited, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := s.ReadHTTPSHistory(limited, q); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("actual history read escaped its read-only pool or context bound", err)
	}
	if reader.Stats().OpenConnections != 1 {
		t.Fatal("unbounded history pool")
	}
}

func TestHTTPSHistoryAllowsBoundedUnrelatedAuditEnvelopes(t *testing.T) {
	s, q, e := httpsHistoryStore(t)
	insertHTTPSHistory(t, s, e)
	if err := s.InsertAuditEvent(context.Background(), domain.AuditEvent{ID: "audit.other", Kind: strings.Repeat("k", 128), Actor: strings.Repeat("a", 128), OccurredAt: q.AsOf, SchemaVersion: 1, Payload: json.RawMessage(`{}`), Retention: domain.RetentionAudit}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ReadHTTPSHistory(context.Background(), q)
	if err != nil || len(page.Runs) != 1 {
		t.Fatal("valid unrelated envelope prevented history", err)
	}
}
