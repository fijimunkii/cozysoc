package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

func qualityHistoryStore(t *testing.T) (*Store, GatewayHistoryQuery, []resolverrun.Event) {
	t.Helper()
	s, q, g := historyStore(t)
	insertHistory(t, s, g)
	at := g[0].At
	base := resolverrun.Event{SchemaVersion: 1, RunID: strings.Repeat("b", 32), Profile: resolverrun.Profile, Selection: nq.ResolverSelection{ID: "selection.test", ResolverID: "resolver.test", QueryID: "query.test", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Observer: nq.Observer{ScopeID: q.ScopeID, SensorID: "resolver-audit", InterfaceName: "en0", InterfaceIndex: 7}}
	a, d, f := base, base, base
	a.State, a.At = "authorized", at
	d.State, d.At = "admitted", at
	f.State, f.At, f.Outcome = "finished", at.Add(3*time.Second), "completed"
	f.Measurement = &resolverrun.Measurement{StartedAt: at, CompletedAt: f.At, Exchange: nq.DNSTimeout, Request: nq.DNSRequestAccepted}
	return s, q, []resolverrun.Event{a, d, f}
}
func TestQualityHistoryReadsBothLayersAndScopesWithoutWrites(t *testing.T) {
	s, q, e := qualityHistoryStore(t)
	insertDNSHistory(t, s, e)
	first, err := s.ReadQualityHistory(context.Background(), q.ScopeID, q.AsOf)
	if err != nil || len(first.Gateway.Runs) != 1 || len(first.Resolver.Runs) != 1 || !first.Gateway.Runs[0].TerminalRetained || !first.Resolver.Runs[0].TerminalRetained {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := s.ReadQualityHistory(context.Background(), q.ScopeID, q.AsOf.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(first, second) || gatewayAuditCount(t, s) != 3 || resolverAuditCount(t, s) != 3 {
		t.Fatal("read mutated retained evidence", err)
	}
	if _, err := s.ReadQualityHistory(context.Background(), "scope.other", q.AsOf); err == nil {
		t.Fatal("scope override exposed evidence")
	}
	if _, err := s.conn.ExecContext(context.Background(), `UPDATE audit_events SET payload='{}' WHERE id=?`, "audit.resolver-run."+e[0].RunID+".finished"); err != nil {
		t.Fatal(err)
	}
	out, err := s.ReadQualityHistory(context.Background(), q.ScopeID, q.AsOf)
	if err == nil || !reflect.DeepEqual(out, QualityHistoryPage{}) {
		t.Fatal("returned gateway partial success alongside corrupt DNS evidence")
	}
}
func TestQualityHistoryUsesSameReadSnapshotAcrossLayers(t *testing.T) {
	s, q, e := qualityHistoryStore(t)
	insertDNSHistory(t, s, e[:2])
	ctx := context.Background()
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	g, err := readGatewayHistoryTx(ctx, tx, q, q.AsOf.UnixNano(), q.AsOf.Add(-GatewayHistoryWindow))
	if err != nil || len(g.Runs) != 1 {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.InsertResolverRunAudit(ctx, e[2]) }()
	// The resolver helper must see the same snapshot established by gateway read,
	// regardless of whether the separate writer is blocked or has committed.
	d, err := readResolverHistoryTx(ctx, tx, ResolverHistoryQuery{ScopeID: q.ScopeID, AsOf: q.AsOf}, q.AsOf.UnixNano(), q.AsOf.Add(-ResolverHistoryWindow))
	if err != nil || len(d.Runs) != 1 || d.Runs[0].TerminalRetained {
		t.Fatal("mixed audit snapshots", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_events`); err == nil {
		t.Fatal("history connection can write")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	page, err := s.ReadQualityHistory(ctx, q.ScopeID, q.AsOf)
	if err != nil || !page.Resolver.Runs[0].TerminalRetained || resolverAuditCount(t, s) != 3 {
		t.Fatal("read rollback absorbed writer audit", err)
	}
}
func TestQualityHistoryReadPoolDeadline(t *testing.T) {
	s, q, _ := qualityHistoryStore(t)
	conn, err := s.gatewayHistoryDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.ReadQualityHistory(ctx, q.ScopeID, q.AsOf); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("escaped bounded read-only pool", err)
	}
	if s.gatewayHistoryDB.Stats().OpenConnections != 1 {
		t.Fatal("unbounded reader ownership")
	}
}
