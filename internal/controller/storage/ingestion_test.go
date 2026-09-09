package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type fakeIngestionSink struct {
	mu sync.Mutex

	blockObservation bool
	started          chan struct{}
	release          chan struct{}
	startOnce        sync.Once
	observationErr   error
	observationNew   bool
	observedPayload  string
	checkpointCalls  int
	eventKinds       []string
}

func newFakeIngestionSink() *fakeIngestionSink {
	return &fakeIngestionSink{
		started:        make(chan struct{}),
		release:        make(chan struct{}),
		observationNew: true,
	}
}

func (f *fakeIngestionSink) InsertObservation(ctx context.Context, observation domain.Observation) (bool, error) {
	f.startOnce.Do(func() { close(f.started) })
	if f.blockObservation {
		select {
		case <-f.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	f.mu.Lock()
	f.observedPayload = string(observation.Payload)
	err := f.observationErr
	inserted := f.observationNew
	f.mu.Unlock()
	return inserted, err
}

func (f *fakeIngestionSink) SaveCheckpoint(context.Context, domain.IngestionCheckpoint) error {
	f.mu.Lock()
	f.checkpointCalls++
	f.mu.Unlock()
	return nil
}

func (*fakeIngestionSink) InsertIdentityClaim(context.Context, domain.IdentityClaim) error {
	return nil
}
func (*fakeIngestionSink) InsertCoverageSample(context.Context, domain.CoverageSample) error {
	return nil
}
func (*fakeIngestionSink) InsertFinding(context.Context, domain.Finding) error { return nil }
func (*fakeIngestionSink) InsertAuditEvent(context.Context, domain.AuditEvent) error {
	return nil
}

func (f *fakeIngestionSink) recordIngestionEvent(_ context.Context, kind string, _ time.Time, _ json.RawMessage) error {
	f.mu.Lock()
	f.eventKinds = append(f.eventKinds, kind)
	f.mu.Unlock()
	return nil
}

func TestIngestorQueueFullIsExplicitAndVisible(t *testing.T) {
	sink := newFakeIngestionSink()
	sink.blockObservation = true
	ingestor, err := newIngestor(sink, 1, nil)
	if err != nil {
		t.Fatal(err)
	}

	first, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-sink.started
	second, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingestor.TrySubmitObservation(ingestionObservation("obs.3"), nil); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue overflow error = %v, want ErrQueueFull", err)
	}
	if got := ingestor.Stats().Dropped; got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}

	close(sink.release)
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsString(sink.eventKinds, "ingestion-backpressure") {
		t.Fatalf("backpressure event not recorded: %v", sink.eventKinds)
	}
}

func TestBlockingSubmitReportsBackpressureWhenContextEnds(t *testing.T) {
	sink := newFakeIngestionSink()
	sink.blockObservation = true
	ingestor, err := newIngestor(sink, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-sink.started
	second, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.2"), nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := ingestor.SubmitObservation(ctx, ingestionObservation("obs.3"), nil); !errors.Is(err, ErrBackpressure) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked submit error = %v, want backpressure + deadline", err)
	}

	close(sink.release)
	_, _ = first.Wait(context.Background())
	_, _ = second.Wait(context.Background())
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestObservationReplayStillAdvancesCheckpoint(t *testing.T) {
	sink := newFakeIngestionSink()
	sink.observationNew = false
	ingestor, err := newIngestor(sink, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	observation := ingestionObservation("obs.replay")
	checkpoint := domain.IngestionCheckpoint{
		SensorID:  observation.SensorID,
		StreamID:  observation.SourceStream,
		Cursor:    "cursor-2",
		UpdatedAt: observation.IngestedAt,
	}
	receipt, err := ingestor.SubmitObservation(context.Background(), observation, &checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	result, err := receipt.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted {
		t.Fatal("replayed observation reported as newly inserted")
	}
	if sink.checkpointCalls != 1 {
		t.Fatalf("checkpoint calls = %d, want 1", sink.checkpointCalls)
	}
	stats := ingestor.Stats()
	if stats.Deduplicated != 1 || stats.Processed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIngestorDefensivelyCopiesQueuedPayloads(t *testing.T) {
	sink := newFakeIngestionSink()
	sink.blockObservation = true
	ingestor, err := newIngestor(sink, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	observation := ingestionObservation("obs.copy")
	receipt, err := ingestor.SubmitObservation(context.Background(), observation, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-sink.started
	observation.Payload[2] = 'X'
	close(sink.release)
	if _, err := receipt.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sink.observedPayload != `{"address":"192.168.1.20"}` {
		t.Fatalf("queued payload mutated through caller buffer: %q", sink.observedPayload)
	}
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFailureIsReturnedAndRecorded(t *testing.T) {
	sink := newFakeIngestionSink()
	sink.observationErr = errors.New("disk full")
	ingestor, err := newIngestor(sink, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.fail"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receipt.Wait(context.Background()); err == nil || !stringsContain(err.Error(), "disk full") {
		t.Fatalf("write failure = %v", err)
	}
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ingestor.Stats().Failed; got != 1 {
		t.Fatalf("failed = %d, want 1", got)
	}
	if !containsString(sink.eventKinds, "ingestion-write-failed") {
		t.Fatalf("write-failure event not recorded: %v", sink.eventKinds)
	}
}

func TestCloseDrainsAcceptedRecords(t *testing.T) {
	sink := newFakeIngestionSink()
	ingestor, err := newIngestor(sink, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"obs.1", "obs.2", "obs.3"} {
		if _, err := ingestor.SubmitObservation(context.Background(), ingestionObservation(id), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := ingestor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := ingestor.Stats()
	if stats.Accepted != 3 || stats.Processed != 3 || !stats.Closed {
		t.Fatalf("close did not drain queue: %+v", stats)
	}
	if _, err := ingestor.SubmitObservation(context.Background(), ingestionObservation("obs.after-close"), nil); !errors.Is(err, ErrIngestorClosed) {
		t.Fatalf("submit after close = %v, want ErrIngestorClosed", err)
	}
}

func ingestionObservation(id string) domain.Observation {
	now := time.Unix(1_800_000_000, 0).UTC()
	source := now.Add(-time.Second)
	return domain.Observation{
		ID:            id,
		ScopeID:       "scope.home",
		SensorID:      "sensor.desktop",
		Kind:          "neighbor-seen",
		SourceStream:  "neighbor-cache",
		SourceKey:     id,
		SourceEventID: id,
		SourceTime:    &source,
		IngestedAt:    now,
		SchemaVersion: 1,
		Attribution:   "desktop-neighbor-cache",
		Payload:       json.RawMessage(`{"address":"192.168.1.20"}`),
		Retention:     domain.RetentionStandard,
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringsContain(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
