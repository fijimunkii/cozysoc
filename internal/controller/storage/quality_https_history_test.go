package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func qualityHTTPSHistoryStore(t *testing.T) (*Store, GatewayHistoryQuery, []httpsrun.Event) {
	t.Helper()
	s, q, dns := qualityHistoryStore(t)
	insertDNSHistory(t, s, dns)
	base := httpsrun.Event{SchemaVersion: 1, RunID: strings.Repeat("c", 32), Profile: httpsrun.Profile,
		Selection: nq.HTTPSSelection{ID: "selection.https", EndpointID: "endpoint.test", RequestID: "request.test", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 503}, Observer: dns[0].Observer}
	base.Observer.SensorID = "https-audit"
	a, d, f := base, base, base
	a.State, a.At = "authorized", dns[0].At
	d.State, d.At = "admitted", dns[1].At
	f.State, f.At, f.Outcome = "finished", dns[2].At, "completed"
	timing := int64(time.Second)
	f.Measurement = &httpsrun.Measurement{StartedAt: a.At, CompletedAt: f.At, Exchange: nq.HTTPSResponseReceived, Request: nq.HTTPSRequestAccepted, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTimeNanoseconds: &timing}
	return s, q, []httpsrun.Event{a, d, f}
}

func TestQualityHTTPSHistoryPreservesThreeLayersWithoutWrites(t *testing.T) {
	s, q, e := qualityHTTPSHistoryStore(t)
	insertHTTPSHistory(t, s, e)
	ctx := context.Background()
	first, err := s.ReadQualityHistoryWithHTTPS(ctx, q.ScopeID, q.AsOf)
	if err != nil || len(first.Gateway.Runs) != 1 || len(first.Resolver.Runs) != 1 || len(first.HTTPS.Runs) != 1 {
		t.Fatalf("%+v %v", first, err)
	}
	h := first.HTTPS.Runs[0]
	if !h.TerminalRetained || h.Measurement.StatusCode != 503 || h.Assessment.ExpectationMatched == nil || !*h.Assessment.ExpectationMatched || !h.LastAuditAt.Equal(e[2].At) {
		t.Fatal("lost original HTTP status, expectation or time")
	}
	second, err := s.ReadQualityHistoryWithHTTPS(ctx, q.ScopeID, q.AsOf.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(first, second) || gatewayAuditCount(t, s) != 3 || resolverAuditCount(t, s) != 3 || httpsAuditCount(t, s) != 3 {
		t.Fatal("read changed evidence", err)
	}
	pair, err := s.ReadQualityHistory(ctx, q.ScopeID, q.AsOf)
	if err != nil || !reflect.DeepEqual(pair, first.QualityHistoryPage) {
		t.Fatal("existing pair changed", err)
	}
	if _, err := s.ReadQualityHistoryWithHTTPS(ctx, "scope.other", q.AsOf); err == nil {
		t.Fatal("scope override exposed history")
	}
}

func TestQualityHTTPSHistoryCorruptionIsAtomicAndPairIndependent(t *testing.T) {
	for _, kind := range []string{"gateway-run", "resolver-run", "https-run"} {
		t.Run(kind, func(t *testing.T) {
			s, q, e := qualityHTTPSHistoryStore(t)
			insertHTTPSHistory(t, s, e)
			if _, err := s.conn.ExecContext(context.Background(), `UPDATE audit_events SET payload='{}' WHERE kind=?`, kind); err != nil {
				t.Fatal(err)
			}
			out, err := s.ReadQualityHistoryWithHTTPS(context.Background(), q.ScopeID, q.AsOf)
			if err == nil || !reflect.DeepEqual(out, QualityHistoryWithHTTPSPage{}) {
				t.Fatal("published partial success alongside corrupt evidence")
			}
			if kind == "https-run" {
				pair, err := s.ReadQualityHistory(context.Background(), q.ScopeID, q.AsOf)
				if err != nil || len(pair.Gateway.Runs) != 1 || len(pair.Resolver.Runs) != 1 {
					t.Fatal("unused HTTPS evidence broke existing diagnosis input", err)
				}
			}
		})
	}
}

func TestQualityHTTPSHistorySharesSnapshotAndDoesNotAbsorbWriter(t *testing.T) {
	s, q, e := qualityHTTPSHistoryStore(t)
	insertHTTPSHistory(t, s, e[:2])
	ctx := context.Background()
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := readGatewayHistoryTx(ctx, tx, q, q.AsOf.UnixNano(), q.AsOf.Add(-GatewayHistoryWindow)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.InsertHTTPSRunAudit(ctx, e[2]) }()
	// HTTPS must share the snapshot established by the gateway read even while
	// the separate writer is trying to record the missing terminal phase.
	h, err := readHTTPSHistoryTx(ctx, tx, HTTPSHistoryQuery{ScopeID: q.ScopeID, AsOf: q.AsOf}, q.AsOf.UnixNano(), q.AsOf.Add(-HTTPSHistoryWindow))
	if err != nil || len(h.Runs) != 1 || h.Runs[0].TerminalRetained || h.Runs[0].Measurement != nil {
		t.Fatal("mixed snapshots", err)
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
	page, err := s.ReadQualityHistoryWithHTTPS(ctx, q.ScopeID, q.AsOf)
	if err != nil || len(page.HTTPS.Runs) != 1 || !page.HTTPS.Runs[0].TerminalRetained || httpsAuditCount(t, s) != 3 {
		t.Fatal("reader rollback absorbed terminal audit", err)
	}
}

func TestQualityHTTPSHistoryUsesOneRetentionCutoff(t *testing.T) {
	s, q, e := qualityHTTPSHistoryStore(t)
	insertHTTPSHistory(t, s, e)
	if _, err := s.conn.ExecContext(context.Background(), `UPDATE audit_events SET expires_at_ns=?`, q.AsOf.UnixNano()); err != nil {
		t.Fatal(err)
	}
	clockReads := 0
	s.now = func() time.Time {
		clockReads++
		return q.AsOf
	}
	// Backdating as-of to before expiry cannot revive any of the three layers.
	page, err := s.ReadQualityHistoryWithHTTPS(context.Background(), q.ScopeID, q.AsOf.Add(-time.Second))
	if err != nil || clockReads != 1 || len(page.Gateway.Runs)+len(page.Resolver.Runs)+len(page.HTTPS.Runs) != 0 {
		t.Fatal("resurrected expired evidence", err)
	}
	if gatewayAuditCount(t, s)+resolverAuditCount(t, s)+httpsAuditCount(t, s) != 9 {
		t.Fatal("history read pruned audits")
	}
}

func TestQualityHTTPSHistoryPreservesMissingEvidence(t *testing.T) {
	s, q, e := qualityHTTPSHistoryStore(t)
	ctx := context.Background()
	for phases := 0; phases <= 2; phases++ {
		if phases > 0 {
			insertHTTPSHistory(t, s, e[phases-1:phases])
		}
		page, err := s.ReadQualityHistoryWithHTTPS(ctx, q.ScopeID, q.AsOf)
		if err != nil || len(page.Gateway.Runs) != 1 || len(page.Resolver.Runs) != 1 || page.HTTPS.Truncated || page.HTTPS.ScanTruncated {
			t.Fatal("missing HTTPS evidence discarded other layers", err)
		}
		if phases == 0 {
			if page.HTTPS.Runs == nil || len(page.HTTPS.Runs) != 0 {
				t.Fatal("absent HTTPS history not explicit")
			}
		} else if len(page.HTTPS.Runs) != 1 || page.HTTPS.Runs[0].TerminalRetained || page.HTTPS.Runs[0].Measurement != nil || page.HTTPS.Runs[0].Outcome != "unknown" {
			t.Fatal("missing terminal became measured evidence")
		}
	}
}

func TestQualityHTTPSHistoryPreservesPerLayerBounds(t *testing.T) {
	s, q, e := qualityHTTPSHistoryStore(t)
	for i := 0; i <= MaxHTTPSHistoryRuns; i++ {
		event := e[0]
		event.RunID = fmt.Sprintf("%032x", i)
		insertHTTPSHistory(t, s, []httpsrun.Event{event})
	}
	page, err := s.ReadQualityHistoryWithHTTPS(context.Background(), q.ScopeID, q.AsOf)
	if err != nil || !page.HTTPS.Truncated || len(page.HTTPS.Runs) != MaxHTTPSHistoryRuns || page.Gateway.Truncated || page.Resolver.Truncated {
		t.Fatal("lost layer-specific run bound", err)
	}
	for i := 0; i <= MaxHTTPSHistoryScan; i++ {
		if err := s.InsertAuditEvent(context.Background(), domain.AuditEvent{ID: fmt.Sprintf("audit.other.%04d", i), Kind: "other", Actor: "controller", OccurredAt: q.AsOf, SchemaVersion: 1, Payload: json.RawMessage(`{}`), Retention: domain.RetentionAudit}); err != nil {
			t.Fatal(err)
		}
	}
	page, err = s.ReadQualityHistoryWithHTTPS(context.Background(), q.ScopeID, q.AsOf)
	if err != nil || !page.Gateway.ScanTruncated || !page.Resolver.ScanTruncated || !page.HTTPS.ScanTruncated || len(page.Gateway.Runs)+len(page.Resolver.Runs)+len(page.HTTPS.Runs) != 0 {
		t.Fatal("scan starvation became exhaustive empty history", err)
	}
}

func TestQualityHTTPSHistorySharesBoundedReadPool(t *testing.T) {
	s, q, _ := qualityHTTPSHistoryStore(t)
	conn, err := s.gatewayHistoryDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	page, err := s.ReadQualityHistoryWithHTTPS(ctx, q.ScopeID, q.AsOf)
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(page, QualityHistoryWithHTTPSPage{}) || s.gatewayHistoryDB.Stats().OpenConnections != 1 {
		t.Fatal("escaped bounded pool or published partial data", err)
	}
}
