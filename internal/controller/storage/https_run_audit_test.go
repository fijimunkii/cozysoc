package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type httpsExecutor func(context.Context, httpsrun.Request) (nq.HTTPSMeasurement, error)

func (f httpsExecutor) ExecuteHTTPS(ctx context.Context, r httpsrun.Request) (nq.HTTPSMeasurement, error) {
	return f(ctx, r)
}
func httpsAuditControl(t *testing.T, store *Store, now *time.Time, execute func(context.Context)) *httpsrun.Control {
	t.Helper()
	c, e := httpsrun.New(httpsrun.Dependencies{Now: func() time.Time { return *now }, Auditor: store,
		Preflight: func(context.Context, string) (httpsrun.Selection, error) {
			p, e := httpsplan.New(httpsplan.Binding{Observer: nq.Observer{ScopeID: "home", SensorID: "local", InterfaceName: "fixture0", InterfaceIndex: 7}, Prefixes: []string{"192.0.2.0/24"}, Source: netip.MustParseAddr("192.0.2.10")}, httpsplan.Configuration{Selection: nq.HTTPSSelection{ID: "https1", EndpointID: "endpoint-v1", RequestID: "request-v1", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: "private.example", RequestTarget: "/check", DestinationPolicy: httpsplan.ExactEndpoint}, *now)
			return httpsrun.Selection{Plan: p, RouteObservedAt: *now, RouteFreshUntil: now.Add(30 * time.Second)}, e
		},
		Executor: httpsExecutor(func(ctx context.Context, r httpsrun.Request) (nq.HTTPSMeasurement, error) {
			execute(ctx)
			if ctx.Err() != nil {
				return nq.HTTPSMeasurement{}, ctx.Err()
			}
			d := r.Selection.Plan.Disclosure()
			start := *now
			*now = now.Add(10 * time.Millisecond)
			zero := time.Duration(0)
			return nq.HTTPSMeasurement{ID: r.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: *now, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTime: &zero}, nil
		})})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c
}
func httpsAuditCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if e := s.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_events WHERE kind=?`, HTTPSRunAuditKind).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// Synthetic selection/executor, real SQLite admission, persistence and restart.
func TestHTTPSAuditDurabilityAndNoRecoveredAuthority(t *testing.T) {
	store, dir := gatewayAuditStore(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	calls := 0
	c := httpsAuditControl(t, store, &now, func(context.Context) {
		calls++
		if httpsAuditCount(t, store) != 2 {
			t.Fatal("executor ran before durable admission")
		}
	})
	now = now.Add(httpsrun.RunInterval)
	r, e := c.Prepare(context.Background(), "https1")
	if e != nil {
		t.Fatal(e)
	}
	if httpsAuditCount(t, store) != 0 {
		t.Fatal("review persisted consent")
	}
	result, e := c.Run(context.Background(), r.Ticket, true)
	if e != nil || calls != 1 || result.Sample == nil || result.Sample.StatusCode != 503 {
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
	if httpsAuditCount(t, reopened) != 3 {
		t.Fatal("lost durable lifecycle")
	}
	var raw, actor, retention string
	var expiry int64
	if e := reopened.conn.QueryRowContext(context.Background(), `SELECT payload,actor,retention_class,expires_at_ns FROM audit_events WHERE id=?`, "audit.https-run."+result.RunID+".finished").Scan(&raw, &actor, &retention, &expiry); e != nil {
		t.Fatal(e)
	}
	var event httpsrun.Event
	if e := json.Unmarshal([]byte(raw), &event); e != nil || httpsrun.ValidateEvent(event) != nil || event.Measurement == nil || event.Measurement.StatusCode != 503 || event.Measurement.ResponseTimeNanoseconds == nil || *event.Measurement.ResponseTimeNanoseconds != 0 {
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
	if e := reopened.InsertHTTPSRunAudit(context.Background(), event); e == nil {
		t.Fatal("duplicate phase accepted")
	}
	restarted := httpsAuditControl(t, reopened, &now, func(context.Context) { t.Fatal("restart replayed execution") })
	if _, e := restarted.Run(context.Background(), r.Ticket, true); !errors.Is(e, httpsrun.ErrReview) {
		t.Fatal(e)
	}
	if _, e := restarted.Prepare(context.Background(), "https1"); !errors.Is(e, httpsrun.ErrCooldown) {
		t.Fatal("restart skipped quiet minute", e)
	}
	var observations, coverage int
	if e := reopened.conn.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM observations),(SELECT count(*) FROM coverage_samples)`).Scan(&observations, &coverage); e != nil || observations != 0 || coverage != 0 {
		t.Fatal("audit became coverage", e)
	}
}

func TestHTTPSAuditSQLiteFailuresLockAdmission(t *testing.T) {
	statements := map[string]string{
		"authorized": `CREATE TRIGGER deny_https_audit BEFORE INSERT ON audit_events WHEN NEW.kind='https-run' AND json_extract(NEW.payload,'$.state')='authorized' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
		"admitted":   `CREATE TRIGGER deny_https_audit BEFORE INSERT ON audit_events WHEN NEW.kind='https-run' AND json_extract(NEW.payload,'$.state')='admitted' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
		"finished":   `CREATE TRIGGER deny_https_audit BEFORE INSERT ON audit_events WHEN NEW.kind='https-run' AND json_extract(NEW.payload,'$.state')='finished' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
	}
	for phase, sql := range statements {
		t.Run(phase, func(t *testing.T) {
			s, _ := gatewayAuditStore(t)
			now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			calls := 0
			c := httpsAuditControl(t, s, &now, func(context.Context) { calls++ })
			if _, e := s.conn.ExecContext(context.Background(), sql); e != nil {
				t.Fatal(e)
			}
			now = now.Add(httpsrun.RunInterval)
			r, e := c.Prepare(context.Background(), "https1")
			if e != nil {
				t.Fatal(e)
			}
			result, e := c.Run(context.Background(), r.Ticket, true)
			if !errors.Is(e, httpsrun.ErrAudit) || result != (httpsrun.Result{}) || strings.Contains(e.Error(), "synthetic") {
				t.Fatalf("%+v %v", result, e)
			}
			want := 0
			if phase == "finished" {
				want = 1
			}
			if calls != want {
				t.Fatal("executed after failed audit")
			}
			if _, e := s.conn.ExecContext(context.Background(), `DROP TRIGGER deny_https_audit`); e != nil {
				t.Fatal(e)
			}
			now = now.Add(time.Hour)
			if _, e := c.Prepare(context.Background(), "https1"); !errors.Is(e, httpsrun.ErrAudit) {
				t.Fatal("audit fault self-recovered", e)
			}
		})
	}
}

func TestHTTPSTerminalAuditOnCancellationAndInvalidEvents(t *testing.T) {
	s, _ := gatewayAuditStore(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := httpsAuditControl(t, s, &now, func(context.Context) { cancel() })
	now = now.Add(httpsrun.RunInterval)
	r, e := c.Prepare(ctx, "https1")
	if e != nil {
		t.Fatal(e)
	}
	result, e := c.Run(ctx, r.Ticket, true)
	if !errors.Is(e, context.Canceled) || result.Outcome != "canceled" || httpsAuditCount(t, s) != 3 {
		t.Fatalf("%+v %v", result, e)
	}
	if e := s.InsertHTTPSRunAudit(context.Background(), httpsrun.Event{}); e == nil || httpsAuditCount(t, s) != 3 {
		t.Fatal("accepted invalid event")
	}
	var missing *Store
	if e := missing.InsertHTTPSRunAudit(context.Background(), httpsrun.Event{}); !errors.Is(e, httpsrun.ErrAudit) {
		t.Fatal(e)
	}
}
