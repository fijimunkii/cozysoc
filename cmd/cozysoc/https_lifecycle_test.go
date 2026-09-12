package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

type httpsExecutorFunc func(context.Context, httpsrun.Request) (nq.HTTPSMeasurement, error)

func (f httpsExecutorFunc) ExecuteHTTPS(ctx context.Context, r httpsrun.Request) (nq.HTTPSMeasurement, error) {
	return f(ctx, r)
}

func TestHTTPSRetirementBetweenReviewAndRunBlocksExecution(t *testing.T) {
	ctx := context.Background()
	h, s, _ := httpsReviewHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	id := saveHTTPS(t, h)
	calls := 0
	if err := h.httpsRuns.install(httpsrun.Dependencies{Now: h.now, Auditor: s, Preflight: h.preflightHTTPSRun, Executor: httpsExecutorFunc(func(context.Context, httpsrun.Request) (nq.HTTPSMeasurement, error) {
		calls++
		return nq.HTTPSMeasurement{}, errors.New("unexpected execution")
	})}); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	review, err := h.httpsRuns.prepare(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetireHTTPSConfiguration(ctx, id); err != nil {
		t.Fatal(err)
	}
	result, err := h.httpsRuns.run(ctx, review.Ticket, true)
	if err != httpsrun.ErrPreflight || result.Outcome != "blocked" || calls != 0 {
		t.Fatal("retired review executed", err)
	}
}

func TestHTTPSShutdownJoinsBeforeStorageMayClose(t *testing.T) {
	ctx := context.Background()
	h, s, _ := httpsReviewHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	id := saveHTTPS(t, h)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	deps := httpsrun.Dependencies{Now: h.now, Auditor: s, Preflight: h.preflightHTTPSRun, Executor: httpsExecutorFunc(func(ctx context.Context, _ httpsrun.Request) (nq.HTTPSMeasurement, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nq.HTTPSMeasurement{}, ctx.Err()
	})}
	if err := h.httpsRuns.install(deps); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	r, err := h.httpsRuns.prepare(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := h.httpsRuns.run(ctx, r.Ticket, true); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not start")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := h.httpsRuns.shutdown(canceled); err != context.Canceled {
		t.Fatal("unfinished run reported drained", err)
	}
	if err := h.httpsRuns.install(deps); err != httpsrun.ErrUnavailable {
		t.Fatal("draining owner replaced")
	}
	if _, err := h.httpsRuns.prepare(ctx, id); err != httpsrun.ErrUnavailable {
		t.Fatal("draining owner admitted")
	}
	close(release)
	if err := h.httpsRuns.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
}

func TestHTTPSLifecycleSettingsAndPreviewStayInert(t *testing.T) {
	ctx := context.Background()
	h, s, calls := httpsReviewHandler(t)
	if err := h.startHTTPSRuns(s); err != nil {
		t.Fatal(err)
	}
	first, err := h.httpsRuns.current()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.startHTTPSRuns(s); err != httpsrun.ErrUnavailable {
		t.Fatal("replaced singleton", err)
	}
	id := saveHTTPS(t, h)
	listed, err := h.ListHTTPSSettings(ctx)
	if err != nil || len(listed.Items) != 1 || listed.ConsentGranted || *calls != 0 {
		t.Fatal("settings acquired authority", err)
	}
	preview, err := h.PreviewHTTPS(ctx, api.HTTPSIDParams{SelectionID: id})
	if err != nil || preview.Mode != "preview-only" || preview.ConsentGranted || preview.ExecutionAvailable || *calls != 1 {
		t.Fatal("preview acquired authority", err)
	}
	if _, err := h.httpsRuns.prepare(ctx, id); err != httpsrun.ErrCooldown {
		t.Fatal("preview changed startup quiet period", err)
	}
	current, err := h.httpsRuns.current()
	if err != nil || first != current {
		t.Fatal("owner changed", err)
	}
	if err := h.httpsRuns.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.startHTTPSRuns(s); err != httpsrun.ErrUnavailable {
		t.Fatal("closed owner reinstalled", err)
	}
}

func TestHTTPSLifecycleUnavailableDependencies(t *testing.T) {
	var missing *controllerAPIHandler
	if err := missing.startHTTPSRuns(nil); err != httpsrun.ErrUnavailable {
		t.Fatal(err)
	}
	var owner httpsRunLifecycle
	if _, err := owner.current(); err != httpsrun.ErrUnavailable {
		t.Fatal(err)
	}
	if err := owner.install(httpsrun.Dependencies{}); err != httpsrun.ErrUnavailable {
		t.Fatal(err)
	}
	if err := owner.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type httpsLifecycleAudit struct {
	delegate httpsrun.Auditor
	events   []httpsrun.Event
}

func (a *httpsLifecycleAudit) InsertHTTPSRunAudit(ctx context.Context, e httpsrun.Event) error {
	if err := a.delegate.InsertHTTPSRunAudit(ctx, e); err != nil {
		return err
	}
	a.events = append(a.events, e)
	return nil
}

func TestHTTPSLifecycleInternalRunUsesDurableAudit(t *testing.T) {
	ctx := context.Background()
	h, s, inspections := httpsReviewHandler(t)
	at := h.now()
	h.now = func() time.Time { return at }
	id := saveHTTPS(t, h)
	audit := &httpsLifecycleAudit{delegate: s}
	deps := httpsrun.Dependencies{Now: h.now, Preflight: h.preflightHTTPSRun, Auditor: audit, Executor: httpsExecutorFunc(func(ctx context.Context, r httpsrun.Request) (nq.HTTPSMeasurement, error) {
		if len(audit.events) != 2 || audit.events[0].State != "authorized" || audit.events[1].State != "admitted" {
			t.Fatal("execution preceded durable admission")
		}
		d := r.Selection.Plan.Disclosure()
		start := at
		elapsed := time.Millisecond
		at = at.Add(elapsed)
		return nq.HTTPSMeasurement{ID: r.MeasurementID, Selection: d.Configuration.Selection, Observer: d.Binding.Observer, StartedAt: start, CompletedAt: at, Stage: nq.HTTPSRequest, Request: nq.HTTPSRequestAccepted, Exchange: nq.HTTPSResponseReceived, StatusCode: 204, ResponseTime: &elapsed}, nil
	})}
	if err := h.httpsRuns.install(deps); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	preview, err := h.PreviewHTTPS(ctx, api.HTTPSIDParams{SelectionID: id})
	if err != nil || preview.ConsentGranted || len(audit.events) != 0 {
		t.Fatal("preview persisted consent", err)
	}
	r, err := h.httpsRuns.prepare(ctx, id)
	if err != nil || len(audit.events) != 0 {
		t.Fatal("prepare persisted consent", err)
	}
	result, err := h.httpsRuns.run(ctx, r.Ticket, true)
	if err != nil || result.Outcome != "completed" || result.Sample == nil || len(audit.events) != 3 || audit.events[2].State != "finished" || *inspections != 3 {
		t.Fatalf("invalid lifecycle: %+v %v", result, err)
	}
	if _, err := h.httpsRuns.run(ctx, r.Ticket, true); err != httpsrun.ErrReview {
		t.Fatal("replayed ticket", err)
	}
	if _, err := h.httpsRuns.prepare(ctx, id); err != httpsrun.ErrCooldown {
		t.Fatal("lost cooldown", err)
	}
}
