package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type gatewayAuditExecutor func(context.Context, gatewayrun.Selection) (gatewayicmp.Sample, error)

func (f gatewayAuditExecutor) ExecuteGateway(ctx context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
	return f(ctx, s)
}

// All targets and preflight results are synthetic. The real SQLite store is the
// only external collaborator: these tests contain no route, ICMP or network I/O.
func gatewayAuditControl(t *testing.T, store *Store, now *time.Time, execute func(context.Context, gatewayrun.Selection) error) *gatewayrun.Control {
	t.Helper()
	c, err := gatewayrun.New(gatewayrun.Dependencies{
		Now: func() time.Time { return *now }, Auditor: store, Executor: gatewayAuditExecutor(func(ctx context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
			if err := execute(ctx, s); err != nil {
				return gatewayicmp.Sample{}, err
			}
			if ctx.Err() != nil {
				return gatewayicmp.Sample{}, ctx.Err()
			}
			start := *now
			*now = now.Add(3 * time.Second)
			return gatewayicmp.Sample{ScopeID: s.Plan.Binding.ScopeID, InterfaceName: s.Plan.Binding.InterfaceName,
				InterfaceIndex: s.Plan.Binding.InterfaceIndex, Target: s.Plan.Target, Source: s.Source, StartedAt: start,
				CompletedAt: *now, SendCalls: 3, AcceptedRequests: 3, Timeouts: 3, Complete: true}, nil
		}),
		Preflight: func(_ context.Context, target netip.Addr) (gatewayrun.Selection, error) {
			p, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{ScopeID: "scope.fixture", InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}, target.String(), *now)
			return gatewayrun.Selection{Plan: p, Source: netip.MustParseAddr("192.168.50.23"), RouteObservedAt: *now, RouteFreshUntil: now.Add(30 * time.Second)}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func gatewayAuditStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dir
}
func gatewayAuditCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM audit_events WHERE kind = ?`, GatewayRunAuditKind).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestGatewayRunRequiresDurableAdmissionAndPersistsNoAuthority(t *testing.T) {
	store, dir := gatewayAuditStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	schema, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	c := gatewayAuditControl(t, store, &now, func(_ context.Context, _ gatewayrun.Selection) error {
		executions++
		if n := gatewayAuditCount(t, store); n != 2 {
			t.Fatalf("executor admitted before durable audit: %d", n)
		}
		return nil
	})
	now = now.Add(gatewayrun.RunInterval)
	review, err := c.Prepare(ctx, netip.MustParseAddr("192.168.50.1"))
	if err != nil {
		t.Fatal(err)
	}
	if gatewayAuditCount(t, store) != 0 {
		t.Fatal("review recorded consent")
	}
	result, err := c.Run(ctx, review.Ticket, true)
	if err != nil || result.Outcome != "completed" || executions != 1 || gatewayAuditCount(t, store) != 3 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := c.Run(ctx, review.Ticket, true); !errors.Is(err, gatewayrun.ErrReview) {
		t.Fatal("ticket replay", err)
	}
	var encoded, actor, retention string
	var expiry int64
	if err := store.conn.QueryRowContext(ctx, `SELECT payload,actor,retention_class,expires_at_ns FROM audit_events WHERE id = ?`, "audit.gateway-run."+result.RunID+".authorized").Scan(&encoded, &actor, &retention, &expiry); err != nil {
		t.Fatal(err)
	}
	if actor != "local-os-user" || retention != "audit" || expiry <= now.UnixNano() {
		t.Fatal("missing audit identity/retention")
	}
	var event gatewayrun.Event
	if err := json.Unmarshal([]byte(encoded), &event); err != nil || gatewayrun.ValidateEvent(event) != nil || event.ScopeID != "scope.fixture" || event.Target != "192.168.50.1" || event.Profile != gatewayrun.Profile {
		t.Fatalf("bad persistent evidence: %+v %v", event, err)
	}
	for _, forbidden := range []string{"ticket", "session", "payload_bytes", "latency", "internet", "secret", "raw_packet"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatal("unnecessary evidence", forbidden)
		}
	}
	if err := store.InsertGatewayRunAudit(ctx, event); err == nil {
		t.Fatal("duplicate phase silently accepted")
	}
	var observations, coverage int
	if err := store.conn.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM observations),(SELECT count(*) FROM coverage_samples)`).Scan(&observations, &coverage); err != nil {
		t.Fatal(err)
	}
	if observations != 0 || coverage != 0 {
		t.Fatal("run-control audit became monitoring evidence")
	}
	if current, err := store.SchemaVersion(ctx); err != nil || current != schema {
		t.Fatal("unexpected schema migration")
	}
	c.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if gatewayAuditCount(t, reopened) != 3 {
		t.Fatal("lost durable lifecycle")
	}
	restarted := gatewayAuditControl(t, reopened, &now, func(context.Context, gatewayrun.Selection) error {
		t.Fatal("restart replayed authorization")
		return nil
	})
	if _, err := restarted.Run(ctx, review.Ticket, true); !errors.Is(err, gatewayrun.ErrReview) {
		t.Fatal(err)
	}
	if _, err := restarted.Prepare(ctx, netip.MustParseAddr("192.168.50.1")); !errors.Is(err, gatewayrun.ErrCooldown) {
		t.Fatal("restart skipped quiet minute", err)
	}
}

func TestGatewayAuditSQLiteFailuresStopAdmissionAndLockControl(t *testing.T) {
	for _, phase := range []string{"authorized", "admitted", "finished"} {
		t.Run(phase, func(t *testing.T) {
			store, _ := gatewayAuditStore(t)
			ctx := context.Background()
			now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
			executions := 0
			// Fixed test-only SQL strings; no caller-controlled SQL or trigger construction.
			statements := map[string]string{
				"authorized": `CREATE TRIGGER deny_gateway_audit BEFORE INSERT ON audit_events WHEN NEW.kind='gateway-run' AND json_extract(NEW.payload,'$.state')='authorized' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
				"admitted":   `CREATE TRIGGER deny_gateway_audit BEFORE INSERT ON audit_events WHEN NEW.kind='gateway-run' AND json_extract(NEW.payload,'$.state')='admitted' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
				"finished":   `CREATE TRIGGER deny_gateway_audit BEFORE INSERT ON audit_events WHEN NEW.kind='gateway-run' AND json_extract(NEW.payload,'$.state')='finished' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`,
			}
			if _, err := store.conn.ExecContext(ctx, statements[phase]); err != nil {
				t.Fatal(err)
			}
			c := gatewayAuditControl(t, store, &now, func(context.Context, gatewayrun.Selection) error { executions++; return nil })
			now = now.Add(gatewayrun.RunInterval)
			review, err := c.Prepare(ctx, netip.MustParseAddr("192.168.50.1"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.Run(ctx, review.Ticket, true)
			if !errors.Is(err, gatewayrun.ErrAudit) || result != (gatewayrun.Result{}) || strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("%+v %v", result, err)
			}
			want := 0
			if phase == "finished" {
				want = 1
			}
			if executions != want {
				t.Fatal("audit failed but action ran", executions)
			}
			if _, err := store.conn.ExecContext(ctx, `DROP TRIGGER deny_gateway_audit`); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Hour)
			if _, err := c.Prepare(ctx, netip.MustParseAddr("192.168.50.1")); !errors.Is(err, gatewayrun.ErrAudit) {
				t.Fatal("audit uncertainty silently recovered", err)
			}
		})
	}
}
func TestGatewayTerminalAuditSurvivesCallerCancellation(t *testing.T) {
	store, _ := gatewayAuditStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := gatewayAuditControl(t, store, &now, func(context.Context, gatewayrun.Selection) error { cancel(); return nil })
	now = now.Add(gatewayrun.RunInterval)
	review, err := c.Prepare(ctx, netip.MustParseAddr("192.168.50.1"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Run(ctx, review.Ticket, true)
	if !errors.Is(err, context.Canceled) || result.Outcome != "canceled" || gatewayAuditCount(t, store) != 3 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestGatewayAuditRejectsMalformedEventsBeforeStorage(t *testing.T) {
	store, _ := gatewayAuditStore(t)
	if err := store.InsertGatewayRunAudit(context.Background(), gatewayrun.Event{}); err == nil || gatewayAuditCount(t, store) != 0 {
		t.Fatal("invalid audit accepted")
	}
	var missing *Store
	if err := missing.InsertGatewayRunAudit(context.Background(), gatewayrun.Event{}); !errors.Is(err, gatewayrun.ErrAudit) {
		t.Fatal(err)
	}
}

func TestGatewayMeasurementsRoundTripDurablyWithoutNewAuthority(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "complete"}[complete], func(t *testing.T) {
			store, dir := gatewayAuditStore(t)
			now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
			store.now = func() time.Time { return now }
			ctx := context.Background()
			c, err := gatewayrun.New(gatewayrun.Dependencies{Now: func() time.Time { return now }, Auditor: store,
				Preflight: func(_ context.Context, target netip.Addr) (gatewayrun.Selection, error) {
					plan, err := networkquality.PreviewGatewayCheck(networkquality.GatewayPlanBinding{ScopeID: "scope.fixture", InterfaceName: "fixture0", InterfaceIndex: 7, Prefixes: []string{"192.168.50.0/24"}}, target.String(), now)
					return gatewayrun.Selection{Plan: plan, Source: netip.MustParseAddr("192.168.50.23"), RouteObservedAt: now, RouteFreshUntil: now.Add(30 * time.Second)}, err
				}, Executor: gatewayAuditExecutor(func(_ context.Context, s gatewayrun.Selection) (gatewayicmp.Sample, error) {
					sample := gatewayicmp.Sample{ScopeID: s.Plan.Binding.ScopeID, InterfaceName: s.Plan.Binding.InterfaceName, InterfaceIndex: s.Plan.Binding.InterfaceIndex,
						Target: s.Plan.Target, Source: s.Source, StartedAt: now, SendCalls: 1, AcceptedRequests: 1, Replies: 1}
					if !complete {
						return sample, gatewayicmp.ErrBinding
					}
					now = now.Add(3 * time.Second)
					sample.SendCalls = 3
					sample.AcceptedRequests = 3
					sample.Replies = 2
					sample.Timeouts = 1
					sample.Complete = true
					sample.CompletedAt = now
					zero := time.Duration(0)
					sample.MeanRTT = &zero
					return sample, nil
				})})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			now = now.Add(gatewayrun.RunInterval)
			review, err := c.Prepare(ctx, netip.MustParseAddr("192.168.50.1"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.Run(ctx, review.Ticket, true)
			if (err == nil) != complete || result.Sample == nil {
				t.Fatalf("missing measured result: %+v %v", result, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(dir, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			var payload string
			var version int
			if err := reopened.conn.QueryRowContext(ctx, `SELECT payload,schema_version FROM audit_events WHERE id=?`, "audit.gateway-run."+result.RunID+".finished").Scan(&payload, &version); err != nil {
				t.Fatal(err)
			}
			var event gatewayrun.Event
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatal(err)
			}
			if version != gatewayrun.EventSchemaVersion || event.SchemaVersion != version || gatewayrun.ValidateEvent(event) != nil || event.Measurement == nil || event.Measurement.Complete != complete || event.Measurement.Replies != result.Sample.Replies {
				t.Fatalf("measurement or payload version did not survive reopening: %+v", event)
			}
			if complete {
				if event.Measurement.MeanRTTNanoseconds == nil || *event.Measurement.MeanRTTNanoseconds != 0 {
					t.Fatal("lost measured zero")
				}
			} else if event.Measurement.MeanRTTNanoseconds != nil || event.Measurement.CompletedAt != nil {
				t.Fatal("partial gained completed metrics")
			}
			if event.ScopeID != result.Sample.ScopeID || event.Target != result.Sample.Target.String() || event.Source != result.Sample.Source.String() {
				t.Fatal("lost measurement provenance")
			}
			// There is still one terminal row, not a parallel unaudited measurement write.
			if gatewayAuditCount(t, reopened) != 3 {
				t.Fatal("unexpected audit write count")
			}
		})
	}
}
