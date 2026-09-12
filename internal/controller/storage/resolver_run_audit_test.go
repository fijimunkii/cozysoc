package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

type resolverExecutor func(context.Context, resolverrun.Request) (nq.ResolverMeasurement, error)

func (f resolverExecutor) ExecuteResolver(ctx context.Context, r resolverrun.Request) (nq.ResolverMeasurement, error) {
	return f(ctx, r)
}
func resolverAuditControl(t *testing.T, store *Store, now *time.Time, execute func(context.Context)) *resolverrun.Control {
	t.Helper()
	c, e := resolverrun.New(resolverrun.Dependencies{Now: func() time.Time { return *now }, Auditor: store,
		Preflight: func(context.Context, string) (resolverrun.Selection, error) {
			p, e := resolverplan.New(resolverplan.Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, resolverplan.Configuration{Selection: nq.ResolverSelection{ID: "dns1", ResolverID: "resolver-v1", QueryID: "query-v1", Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Endpoint: netip.MustParseAddrPort("192.0.2.53:53"), Name: "private.example.", DestinationScope: resolverplan.EnrolledPrefix}, *now)
			return resolverrun.Selection{Plan: p, RouteObservedAt: *now, RouteFreshUntil: now.Add(30 * time.Second)}, e
		},
		Executor: resolverExecutor(func(ctx context.Context, r resolverrun.Request) (nq.ResolverMeasurement, error) {
			execute(ctx)
			if ctx.Err() != nil {
				return nq.ResolverMeasurement{}, ctx.Err()
			}
			d := r.Selection.Plan.Disclosure()
			start := *now
			*now = now.Add(10 * time.Millisecond)
			zero := time.Duration(0)
			return nq.ResolverMeasurement{ID: r.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: *now, Request: nq.DNSRequestAccepted, Exchange: nq.DNSResponseReceived, Reply: &nq.DNSReply{RCode: 3}, ResponseTime: &zero}, nil
		})})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c
}
func resolverAuditCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if e := s.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_events WHERE kind=?`, ResolverRunAuditKind).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// Synthetic selection/executor, real SQLite admission, persistence and restart.
func TestResolverAuditDurabilityAndNoRecoveredAuthority(t *testing.T) {
	store, dir := gatewayAuditStore(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	calls := 0
	c := resolverAuditControl(t, store, &now, func(context.Context) {
		calls++
		if resolverAuditCount(t, store) != 2 {
			t.Fatal("executor ran before durable admission")
		}
	})
	now = now.Add(resolverrun.RunInterval)
	r, e := c.Prepare(context.Background(), "dns1")
	if e != nil {
		t.Fatal(e)
	}
	if resolverAuditCount(t, store) != 0 {
		t.Fatal("review persisted consent")
	}
	result, e := c.Run(context.Background(), r.Ticket, true)
	if e != nil || calls != 1 || result.Sample == nil || result.Sample.Reply.RCode != 3 {
		t.Fatalf("%+v %v", result, e)
	}
	if e := c.Shutdown(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := store.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(dir, DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if resolverAuditCount(t, reopened) != 3 {
		t.Fatal("lost durable lifecycle")
	}
	var raw, actor, retention string
	var expiry int64
	if e := reopened.conn.QueryRowContext(context.Background(), `SELECT payload,actor,retention_class,expires_at_ns FROM audit_events WHERE id=?`, "audit.resolver-run."+result.RunID+".finished").Scan(&raw, &actor, &retention, &expiry); e != nil {
		t.Fatal(e)
	}
	var event resolverrun.Event
	if e := json.Unmarshal([]byte(raw), &event); e != nil || resolverrun.ValidateEvent(event) != nil || event.Measurement == nil || event.Measurement.Reply.RCode != 3 || event.Measurement.ResponseTimeNanoseconds == nil || *event.Measurement.ResponseTimeNanoseconds != 0 {
		t.Fatalf("lost evidence: %s %v", raw, e)
	}
	if actor != "controller" || retention != "audit" || expiry <= now.UnixNano() {
		t.Fatal("missing retention/actor")
	}
	for _, forbidden := range []string{"private.example", "192.0.2", "ticket", "raw_packet", "secret"} {
		if strings.Contains(raw, forbidden) {
			t.Fatal("retained private wire/authority data")
		}
	}
	if e := reopened.InsertResolverRunAudit(context.Background(), event); e == nil {
		t.Fatal("duplicate phase accepted")
	}
	restarted := resolverAuditControl(t, reopened, &now, func(context.Context) { t.Fatal("restart replayed execution") })
	if _, e := restarted.Run(context.Background(), r.Ticket, true); !errors.Is(e, resolverrun.ErrReview) {
		t.Fatal(e)
	}
	if _, e := restarted.Prepare(context.Background(), "dns1"); !errors.Is(e, resolverrun.ErrCooldown) {
		t.Fatal("restart skipped quiet minute", e)
	}
	var observations, coverage int
	if e := reopened.conn.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM observations),(SELECT count(*) FROM coverage_samples)`).Scan(&observations, &coverage); e != nil || observations != 0 || coverage != 0 {
		t.Fatal("audit became coverage", e)
	}
}

func TestResolverAuditSQLiteFailuresLockAdmission(t *testing.T) {
	statements := map[string]string{
		"authorized": `CREATE TRIGGER deny_resolver_audit BEFORE INSERT ON audit_events WHEN NEW.kind='resolver-run' AND json_extract(NEW.payload,'$.state')='authorized' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
		"admitted":   `CREATE TRIGGER deny_resolver_audit BEFORE INSERT ON audit_events WHEN NEW.kind='resolver-run' AND json_extract(NEW.payload,'$.state')='admitted' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
		"finished":   `CREATE TRIGGER deny_resolver_audit BEFORE INSERT ON audit_events WHEN NEW.kind='resolver-run' AND json_extract(NEW.payload,'$.state')='finished' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
	}
	for phase, sql := range statements {
		t.Run(phase, func(t *testing.T) {
			s, _ := gatewayAuditStore(t)
			now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			calls := 0
			c := resolverAuditControl(t, s, &now, func(context.Context) { calls++ })
			if _, e := s.conn.ExecContext(context.Background(), sql); e != nil {
				t.Fatal(e)
			}
			now = now.Add(resolverrun.RunInterval)
			r, e := c.Prepare(context.Background(), "dns1")
			if e != nil {
				t.Fatal(e)
			}
			result, e := c.Run(context.Background(), r.Ticket, true)
			if !errors.Is(e, resolverrun.ErrAudit) || result != (resolverrun.Result{}) || strings.Contains(e.Error(), "synthetic") {
				t.Fatalf("%+v %v", result, e)
			}
			want := 0
			if phase == "finished" {
				want = 1
			}
			if calls != want {
				t.Fatal("executed after failed audit")
			}
			if _, e := s.conn.ExecContext(context.Background(), `DROP TRIGGER deny_resolver_audit`); e != nil {
				t.Fatal(e)
			}
			now = now.Add(time.Hour)
			if _, e := c.Prepare(context.Background(), "dns1"); !errors.Is(e, resolverrun.ErrAudit) {
				t.Fatal("audit fault self-recovered", e)
			}
		})
	}
}

func TestResolverTerminalAuditOnCancellationAndInvalidEvents(t *testing.T) {
	s, _ := gatewayAuditStore(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := resolverAuditControl(t, s, &now, func(context.Context) { cancel() })
	now = now.Add(resolverrun.RunInterval)
	r, e := c.Prepare(ctx, "dns1")
	if e != nil {
		t.Fatal(e)
	}
	result, e := c.Run(ctx, r.Ticket, true)
	if !errors.Is(e, context.Canceled) || result.Outcome != "canceled" || resolverAuditCount(t, s) != 3 {
		t.Fatalf("%+v %v", result, e)
	}
	if e := s.InsertResolverRunAudit(context.Background(), resolverrun.Event{}); e == nil || resolverAuditCount(t, s) != 3 {
		t.Fatal("accepted invalid event")
	}
	var missing *Store
	if e := missing.InsertResolverRunAudit(context.Background(), resolverrun.Event{}); !errors.Is(e, resolverrun.ErrAudit) {
		t.Fatal(e)
	}
}
