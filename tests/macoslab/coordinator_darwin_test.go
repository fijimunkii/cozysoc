package macoslab

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayroute"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type runAudit struct{ events []gatewayrun.Event }

func (a *runAudit) InsertGatewayRunAudit(_ context.Context, e gatewayrun.Event) error {
	if err := gatewayrun.ValidateEvent(e); err != nil {
		return err
	}
	a.events = append(a.events, e)
	return nil
}

// Exercise the real coordinator, adapter, route checks and sender on the same
// isolated pair. SQLite durability is independently tested in storage. Only the
// initial construction clock is aged to avoid one minute per fixture; all review,
// measurement and audit timestamps subsequently use the real current clock.
type coordinatedRun struct {
	control *gatewayrun.Control
	review  gatewayrun.Review
	audit   *runAudit
}

func prepareCoordinatedRun(t *testing.T, ctx context.Context, r gatewayicmp.Request) *coordinatedRun {
	t.Helper()
	constructed := false
	audit := &runAudit{}
	c, err := gatewayrun.New(gatewayrun.Dependencies{
		Now: func() time.Time {
			now := time.Now()
			if !constructed {
				return now.Add(-gatewayrun.RunInterval)
			}
			return now
		},
		Auditor: audit, Executor: gatewayrun.NewICMPExecutor(gatewayicmp.NewCandidate()),
		Preflight: func(ctx context.Context, target netip.Addr) (gatewayrun.Selection, error) {
			plan, err := networkquality.PreviewGatewayCheck(r.Plan.Binding, target.String(), time.Now())
			if err != nil {
				return gatewayrun.Selection{}, err
			}
			e, err := gatewayroute.NewInspector().Inspect(ctx, plan.Binding, target)
			if err != nil {
				return gatewayrun.Selection{}, err
			}
			return gatewayrun.Selection{Plan: plan, Source: e.SourceAddress, RouteObservedAt: e.ObservedAt, RouteFreshUntil: e.FreshUntil}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	constructed = true
	t.Cleanup(c.Close)
	review, err := c.Prepare(ctx, r.Plan.Target)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 0 {
		t.Fatal("review recorded consent")
	}
	return &coordinatedRun{control: c, review: review, audit: audit}
}

// Assertions run on the testing goroutine, even for a canceled asynchronous run.
func (r *coordinatedRun) sample(t *testing.T, result gatewayrun.Result, runErr error) gatewayicmp.Sample {
	t.Helper()
	c, review, audit := r.control, r.review, r.audit
	if len(audit.events) != 3 || audit.events[0].State != "authorized" || audit.events[1].State != "admitted" || audit.events[2].State != "finished" {
		t.Fatalf("missing controlled lifecycle: %+v %v", audit.events, runErr)
	}
	terminal := audit.events[2]
	if terminal.RunID != result.RunID || terminal.Outcome != result.Outcome || result.Sample == nil || terminal.Measurement == nil {
		t.Fatalf("missing measured terminal result: %+v %v", result, runErr)
	}
	sample := *result.Sample
	m := terminal.Measurement
	if m.Complete != sample.Complete || m.SendCalls != sample.SendCalls || m.AcceptedRequests != sample.AcceptedRequests || m.Replies != sample.Replies || m.Timeouts != sample.Timeouts ||
		!m.StartedAt.Equal(sample.StartedAt) || terminal.Source != source || terminal.Target != target || terminal.InterfaceName != "feth42" {
		t.Fatal("coordinator changed measured evidence")
	}
	if (m.CompletedAt == nil) != sample.CompletedAt.IsZero() || (m.MeanRTTNanoseconds == nil) != (sample.MeanRTT == nil) {
		t.Fatal("lost optional measurement fields")
	}
	if m.CompletedAt != nil && !m.CompletedAt.Equal(sample.CompletedAt) {
		t.Fatal("changed completion time")
	}
	if m.MeanRTTNanoseconds != nil && *m.MeanRTTNanoseconds != int64(*sample.MeanRTT) {
		t.Fatal("changed measured latency")
	}
	if (runErr == nil) != (result.Outcome == "completed") || (runErr == nil && !sample.Complete) {
		t.Fatal("conflated run and measurement outcomes")
	}
	if _, err := c.Run(context.Background(), review.Ticket, true); !errors.Is(err, gatewayrun.ErrReview) {
		t.Fatal("live review replayed")
	}
	if len(audit.events) != 3 {
		t.Fatal("replay changed lifecycle")
	}
	return sample
}

func coordinatedSample(t *testing.T, ctx context.Context, r gatewayicmp.Request) (gatewayicmp.Sample, error) {
	t.Helper()
	run := prepareCoordinatedRun(t, ctx, r)
	result, err := run.control.Run(ctx, run.review.Ticket, true)
	return run.sample(t, result, err), err
}
