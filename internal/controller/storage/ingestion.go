package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const (
	DefaultIngestionCapacity = 256
	MaxIngestionCapacity     = 8192
)

var (
	ErrIngestorClosed = errors.New("ingestor is closed")
	ErrQueueFull      = errors.New("ingestion queue is full")
	ErrBackpressure   = errors.New("ingestion backpressure prevented enqueue")
)

type IngestionKind string

const (
	IngestionObservation   IngestionKind = "observation"
	IngestionIdentityClaim IngestionKind = "identity-claim"
	IngestionCoverage      IngestionKind = "coverage-sample"
	IngestionFinding       IngestionKind = "finding"
	IngestionAudit         IngestionKind = "audit-event"
)

type IngestionResult struct {
	Kind     IngestionKind `json:"kind"`
	Inserted bool          `json:"inserted"`
}

type IngestionReceipt struct {
	state *receiptState
}

type receiptState struct {
	ready      chan struct{}
	accepted   chan struct{}
	acceptedAt time.Time
	outcome    ingestionOutcome
}

func (r IngestionReceipt) Wait(ctx context.Context) (IngestionResult, error) {
	if r.state == nil {
		return IngestionResult{}, fmt.Errorf("invalid ingestion receipt")
	}
	select {
	case <-r.state.ready:
		return r.state.outcome.result, r.state.outcome.err
	case <-ctx.Done():
		return IngestionResult{}, ctx.Err()
	}
}

type IngestionStats struct {
	Capacity     int    `json:"capacity"`
	Depth        int    `json:"depth"`
	Accepted     uint64 `json:"accepted"`
	Processed    uint64 `json:"processed"`
	Deduplicated uint64 `json:"deduplicated"`
	Rejected     uint64 `json:"rejected"`
	Dropped      uint64 `json:"dropped"`
	Failed       uint64 `json:"failed"`
	Closing      bool   `json:"closing"`
	Closed       bool   `json:"closed"`
}

type ingestionSink interface {
	InsertObservation(context.Context, domain.Observation) (bool, error)
	SaveCheckpoint(context.Context, domain.IngestionCheckpoint) error
	InsertIdentityClaim(context.Context, domain.IdentityClaim) error
	InsertCoverageSample(context.Context, domain.CoverageSample) error
	InsertFinding(context.Context, domain.Finding) error
	InsertAuditEvent(context.Context, domain.AuditEvent) error
	recordIngestionEvent(context.Context, string, time.Time, json.RawMessage) error
}

type ingestionItem struct {
	kind        IngestionKind
	observation *domain.Observation
	checkpoint  *domain.IngestionCheckpoint
	claim       *domain.IdentityClaim
	coverage    *domain.CoverageSample
	finding     *domain.Finding
	audit       *domain.AuditEvent
	receipt     *receiptState
}

type ingestionOutcome struct {
	result IngestionResult
	err    error
}

type ingestionEpisode struct {
	kind       string
	occurredAt time.Time
	details    json.RawMessage
}

type Ingestor struct {
	sink       ingestionSink
	queue      chan ingestionItem
	eventQueue chan ingestionEpisode
	logger     *slog.Logger
	now        func() time.Time
	latency    *ingestionLatencyTracker

	stateMu sync.Mutex
	closing bool
	closed  bool
	stats   IngestionStats

	submitters sync.WaitGroup
	closeOnce  sync.Once
	done       chan struct{}

	episodeMu      sync.Mutex
	overflowActive bool
	failureActive  bool
	failureClass   string
}

func NewIngestor(store *Store, capacity int, logger *slog.Logger) (*Ingestor, error) {
	if store == nil {
		return nil, fmt.Errorf("storage is required")
	}
	return newIngestor(store, capacity, logger)
}

func newIngestor(sink ingestionSink, capacity int, logger *slog.Logger) (*Ingestor, error) {
	if sink == nil {
		return nil, fmt.Errorf("ingestion sink is required")
	}
	if capacity == 0 {
		capacity = DefaultIngestionCapacity
	}
	if capacity < 1 || capacity > MaxIngestionCapacity {
		return nil, fmt.Errorf("ingestion capacity must be between 1 and %d", MaxIngestionCapacity)
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ingestor := &Ingestor{
		sink:       sink,
		queue:      make(chan ingestionItem, capacity),
		eventQueue: make(chan ingestionEpisode, 8),
		logger:     logger,
		now:        time.Now,
		latency:    newIngestionLatencyTracker(),
		done:       make(chan struct{}),
		stats: IngestionStats{
			Capacity: capacity,
		},
	}
	go ingestor.run()
	return ingestor, nil
}

func (i *Ingestor) SubmitObservation(ctx context.Context, observation domain.Observation, checkpoint *domain.IngestionCheckpoint) (IngestionReceipt, error) {
	item, err := newObservationItem(observation, checkpoint)
	if err != nil {
		i.noteRejected()
		return IngestionReceipt{}, err
	}
	return i.submit(ctx, item, false)
}

func (i *Ingestor) TrySubmitObservation(observation domain.Observation, checkpoint *domain.IngestionCheckpoint) (IngestionReceipt, error) {
	item, err := newObservationItem(observation, checkpoint)
	if err != nil {
		i.noteRejected()
		return IngestionReceipt{}, err
	}
	return i.submit(context.Background(), item, true)
}

func (i *Ingestor) SubmitIdentityClaim(ctx context.Context, claim domain.IdentityClaim) (IngestionReceipt, error) {
	copyValue := cloneIdentityClaim(claim)
	if err := domain.ValidateIdentityClaim(copyValue); err != nil {
		i.noteRejected()
		return IngestionReceipt{}, err
	}
	return i.submit(ctx, ingestionItem{kind: IngestionIdentityClaim, claim: &copyValue}, false)
}

func (i *Ingestor) SubmitCoverageSample(ctx context.Context, sample domain.CoverageSample) (IngestionReceipt, error) {
	copyValue := cloneCoverageSample(sample)
	if err := domain.ValidateCoverageSample(copyValue); err != nil {
		i.noteRejected()
		return IngestionReceipt{}, err
	}
	return i.submit(ctx, ingestionItem{kind: IngestionCoverage, coverage: &copyValue}, false)
}

func (i *Ingestor) SubmitFinding(ctx context.Context, finding domain.Finding) (IngestionReceipt, error) {
	copyValue := cloneFinding(finding)
	if err := domain.ValidateFinding(copyValue); err != nil {
		i.noteRejected()
		return IngestionReceipt{}, err
	}
	return i.submit(ctx, ingestionItem{kind: IngestionFinding, finding: &copyValue}, false)
}

func (i *Ingestor) SubmitAuditEvent(ctx context.Context, event domain.AuditEvent) (IngestionReceipt, error) {
	copyValue := cloneAuditEvent(event)
	if err := domain.ValidateAuditEvent(copyValue); err != nil {
		i.noteRejected()
		return IngestionReceipt{}, err
	}
	return i.submit(ctx, ingestionItem{kind: IngestionAudit, audit: &copyValue}, false)
}

func (i *Ingestor) Stats() IngestionStats {
	if i == nil {
		return IngestionStats{}
	}
	i.stateMu.Lock()
	defer i.stateMu.Unlock()
	stats := i.stats
	stats.Depth = len(i.queue)
	stats.Closing = i.closing
	stats.Closed = i.closed
	return stats
}

func (i *Ingestor) Close(ctx context.Context) error {
	if i == nil {
		return nil
	}
	i.closeOnce.Do(func() {
		i.stateMu.Lock()
		i.closing = true
		i.stateMu.Unlock()
		go func() {
			i.submitters.Wait()
			close(i.queue)
		}()
	})
	select {
	case <-i.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Ingestor) submit(ctx context.Context, item ingestionItem, nonBlocking bool) (IngestionReceipt, error) {
	if i == nil {
		return IngestionReceipt{}, ErrIngestorClosed
	}
	if err := ctx.Err(); err != nil {
		return IngestionReceipt{}, err
	}
	if !i.beginSubmit() {
		return IngestionReceipt{}, ErrIngestorClosed
	}
	defer i.submitters.Done()

	item.receipt = &receiptState{ready: make(chan struct{}), accepted: make(chan struct{})}
	if nonBlocking {
		select {
		case i.queue <- item:
			i.markAccepted(item.receipt)
			return IngestionReceipt{state: item.receipt}, nil
		default:
			i.noteDropped(item.kind, "queue-full")
			return IngestionReceipt{}, ErrQueueFull
		}
	}

	select {
	case i.queue <- item:
		i.markAccepted(item.receipt)
		return IngestionReceipt{state: item.receipt}, nil
	case <-ctx.Done():
		i.noteDropped(item.kind, "submit-context-ended")
		return IngestionReceipt{}, errors.Join(ErrBackpressure, ctx.Err())
	}
}

func (i *Ingestor) markAccepted(receipt *receiptState) {
	acceptedAt := i.now().UTC()
	receipt.acceptedAt = acceptedAt
	i.latency.accept(receipt, acceptedAt)
	i.noteAccepted()
	close(receipt.accepted)
}

func (i *Ingestor) beginSubmit() bool {
	i.stateMu.Lock()
	defer i.stateMu.Unlock()
	if i.closing || i.closed {
		return false
	}
	i.submitters.Add(1)
	return true
}

func (i *Ingestor) run() {
	defer func() {
		i.stateMu.Lock()
		i.closed = true
		i.stateMu.Unlock()
		close(i.done)
	}()
	for {
		select {
		case event := <-i.eventQueue:
			i.writeEpisode(event)
		case item, ok := <-i.queue:
			if !ok {
				i.drainEpisodes()
				return
			}
			<-item.receipt.accepted
			startedAt := i.now().UTC()
			result, err := i.process(item)
			completedAt := i.now().UTC()
			i.latency.complete(item.receipt, item.receipt.acceptedAt, startedAt, completedAt, err == nil)
			if err != nil {
				i.noteFailure(item.kind, err)
			} else {
				i.noteProcessed(result)
			}
			item.receipt.outcome = ingestionOutcome{result: result, err: err}
			close(item.receipt.ready)
			i.maybeEndOverflowEpisode()
		}
	}
}

func (i *Ingestor) drainEpisodes() {
	for {
		select {
		case event := <-i.eventQueue:
			i.writeEpisode(event)
		default:
			return
		}
	}
}

func (i *Ingestor) writeEpisode(event ingestionEpisode) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := i.sink.recordIngestionEvent(ctx, event.kind, event.occurredAt, event.details); err != nil {
		i.logger.Warn("ingestion_event_record_failed", "kind", event.kind)
	}
}

func (i *Ingestor) process(item ingestionItem) (IngestionResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := IngestionResult{Kind: item.kind, Inserted: true}
	switch item.kind {
	case IngestionObservation:
		inserted, err := i.sink.InsertObservation(ctx, *item.observation)
		result.Inserted = inserted
		if err != nil {
			return result, err
		}
		if item.checkpoint != nil {
			if err := i.sink.SaveCheckpoint(ctx, *item.checkpoint); err != nil {
				return result, err
			}
	case IngestionIdentityClaim:
		if err := i.sink.InsertIdentityClaim(ctx, *item.claim); err != nil {
			return result, err
		}
	case IngestionCoverage:
		if err := i.sink.InsertCoverageSample(ctx, *item.coverage); err != nil {
			return result, err
		}
	case IngestionFinding:
		if err := i.sink.InsertFinding(ctx, *item.finding); err != nil {
			return result, err
		}
	case IngestionAudit:
		if err := i.sink.InsertAuditEvent(ctx, *item.audit); err != nil {
			return result, err
		}
	default:
		return result, fmt.Errorf("unknown ingestion kind %q", item.kind)
	}
	return result, nil
}

func (i *Ingestor) noteAccepted() {
	i.stateMu.Lock()
	i.stats.Accepted++
	i.stateMu.Unlock()
}

func (i *Ingestor) noteRejected() {
	if i == nil {
		return
	}
	i.stateMu.Lock()
	defer i.stateMu.Unlock()
	i.stats.Rejected++
}

func (i *Ingestor) noteProcessed(result IngestionResult) {
	i.stateMu.Lock()
	i.stats.Processed++
	if result.Kind == IngestionObservation && !result.Inserted {
		i.stats.Deduplicated++
	}
	i.stateMu.Unlock()

	i.episodeMu.Lock()
	i.failureActive = false
	i.failureClass = ""
	i.episodeMu.Unlock()
}

func (i *Ingestor) noteFailure(kind IngestionKind, err error) {
	i.stateMu.Lock()
	i.stats.Failed++
	failed := i.stats.Failed
	i.stateMu.Unlock()

	failureClass := classifyIngestionFailure(err)
	i.episodeMu.Lock()
	first := !i.failureActive
	i.failureActive = true
	i.failureClass = failureClass
	i.episodeMu.Unlock()
	if first {
		i.recordEpisode("ingestion-write-failed", map[string]any{
			"kind":          kind,
			"failure_class": failureClass,
			"failed_total":  failed,
		})
	}
}

func (i *Ingestor) noteDropped(kind IngestionKind, reason string) {
	i.stateMu.Lock()
	i.stats.Dropped++
	dropped := i.stats.Dropped
	capacity := i.stats.Capacity
	i.stateMu.Unlock()

	i.episodeMu.Lock()
	first := !i.overflowActive
	if first {
		i.overflowActive = true
	}
	i.episodeMu.Unlock()
	if first {
		i.recordEpisode("ingestion-backpressure", map[string]any{
			"kind":          kind,
			"reason":        reason,
			"dropped_total": dropped,
			"capacity":      capacity,
		})
	}
}

func (i *Ingestor) maybeEndOverflowEpisode() {
	if len(i.queue) > cap(i.queue)/2 {
		return
	}
	i.episodeMu.Lock()
	i.overflowActive = false
	i.episodeMu.Unlock()
}

func (i *Ingestor) recordEpisode(kind string, details map[string]any) {
	encoded, err := json.Marshal(details)
	if err != nil {
		return
	}
	event := ingestionEpisode{
		kind:       kind,
		occurredAt: i.now().UTC(),
		details:    encoded,
	}
	select {
	case i.eventQueue <- event:
	default:
		i.logger.Warn("ingestion_event_queue_full", "kind", kind)
	}
}

func (s *Store) recordIngestionEvent(ctx context.Context, kind string, occurredAt time.Time, details json.RawMessage) error {
	return s.insertStorageEvent(ctx, kind, occurredAt, details)
}

func newObservationItem(observation domain.Observation, checkpoint *domain.IngestionCheckpoint) (ingestionItem, error) {
	copyObservation := cloneObservation(observation)
	if err := domain.ValidateObservation(copyObservation); err != nil {
		return ingestionItem{}, err
	}
	item := ingestionItem{kind: IngestionObservation, observation: &copyObservation}
	if checkpoint == nil {
		return item, nil
	}
	copyCheckpoint := *checkpoint
	if err := domain.ValidateCheckpoint(copyCheckpoint); err != nil {
		return ingestionItem{}, err
	}
	if copyCheckpoint.SensorID != copyObservation.SensorID || copyCheckpoint.StreamID != copyObservation.SourceStream {
		return ingestionItem{}, fmt.Errorf("observation checkpoint must match sensor_id and source_stream")
	}
	item.checkpoint = &copyCheckpoint
	return item, nil
}

func cloneObservation(value domain.Observation) domain.Observation {
	out := value
	out.Payload = append(json.RawMessage(nil), value.Payload...)
	out.SourceTime = cloneTime(value.SourceTime)
	out.Confidence = cloneFloat(value.Confidence)
	return out
}

func cloneIdentityClaim(value domain.IdentityClaim) domain.IdentityClaim {
	out := value
	out.ValidUntil = cloneTime(value.ValidUntil)
	out.Confidence = cloneFloat(value.Confidence)
	return out
}

func cloneCoverageSample(value domain.CoverageSample) domain.CoverageSample {
	out := value
	out.Evidence = append(json.RawMessage(nil), value.Evidence...)
	return out
}

func cloneFinding(value domain.Finding) domain.Finding {
	out := value
	out.Payload = append(json.RawMessage(nil), value.Payload...)
	out.EvidenceObservationIDs = append([]string(nil), value.EvidenceObservationIDs...)
	out.Confidence = cloneFloat(value.Confidence)
	return out
}

func cloneAuditEvent(value domain.AuditEvent) domain.AuditEvent {
	out := value
	out.Payload = append(json.RawMessage(nil), value.Payload...)
	return out
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
