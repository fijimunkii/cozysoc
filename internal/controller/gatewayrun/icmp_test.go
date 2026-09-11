package gatewayrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
)

type measureFunc func(context.Context, gatewayicmp.Request) (gatewayicmp.Sample, error)

func (f measureFunc) Measure(ctx context.Context, r gatewayicmp.Request) (gatewayicmp.Sample, error) {
	return f(ctx, r)
}

func TestICMPAdapterPreservesContextSelectionAndSample(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := selectionAt(time.Now().UTC(), testTarget)
	want := completeSample(s, s.Plan.CreatedAt, 0)
	cause := errors.New("trusted diagnostic; coordinator must sanitize it")
	calls := 0
	e := icmpExecutor{sender: measureFunc(func(got context.Context, r gatewayicmp.Request) (gatewayicmp.Sample, error) {
		calls++
		if got != ctx || r.Source != s.Source || r.Plan.Target != s.Plan.Target || r.Plan.Budget != s.Plan.Budget {
			t.Fatal("adapter changed authority or deadline")
		}
		r.Plan.Binding.Prefixes[0] = "10.0.0.0/8"
		return want, cause
	})}
	sample, err := e.ExecuteGateway(ctx, s)
	if err != cause || sample != want || calls != 1 || s.Plan.Binding.Prefixes[0] != "192.168.50.0/24" {
		t.Fatal("adapter lost evidence or shared inputs")
	}
	if NewICMPExecutor(nil) != nil {
		t.Fatal("nil candidate enabled fallback")
	}
	if NewICMPExecutor(gatewayicmp.NewCandidate()) == nil {
		t.Fatal("candidate adapter unavailable")
	}
}

func TestAdapterAndCoordinatorDoNotRetryOrLeakPanicEvidence(t *testing.T) {
	c, _, audit, _ := fixture(t)
	calls := 0
	c.deps.Executor = icmpExecutor{sender: measureFunc(func(context.Context, gatewayicmp.Request) (gatewayicmp.Sample, error) {
		calls++
		panic("private transport failure")
	})}
	r := prepare(t, c)
	got, err := c.Run(context.Background(), r.Ticket, true)
	if err != ErrExecution || got.Outcome != "indeterminate" || got.Sample != nil || calls != 1 || audit.copy()[2].Measurement != nil {
		t.Fatalf("%+v %v", got, err)
	}
}

type panicMatchError struct{}

func (panicMatchError) Error() string { return "private diagnostic" }
func (panicMatchError) Is(error) bool { panic("private error classification") }

func TestErrorClassificationPanicIsIndeterminateAndCannotPublish(t *testing.T) {
	c, _, audit, _ := fixture(t)
	c.deps.Executor = executorFunc(func(context.Context, Selection) (gatewayicmp.Sample, error) {
		return gatewayicmp.Sample{}, panicMatchError{}
	})
	r := prepare(t, c)
	got, err := c.Run(context.Background(), r.Ticket, true)
	if err != ErrExecution || got.Outcome != "indeterminate" || got.Sample != nil || audit.copy()[2].Measurement != nil {
		t.Fatalf("%+v %v", got, err)
	}
}
