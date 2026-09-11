package gatewayrun

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
)

func TestMeasuredResultsAreSeparateFromExecutionAndCommittedWithAudit(t *testing.T) {
	for replies := 0; replies <= 3; replies++ {
		t.Run(string(rune('0'+replies)), func(t *testing.T) {
			c, clock, audit, _ := fixture(t)
			c.deps.Executor = executorFunc(func(_ context.Context, s Selection) (gatewayicmp.Sample, error) {
				sample := completeSample(s, clock.now(), replies)
				if replies > 0 {
					zero := time.Duration(0)
					sample.MeanRTT = &zero
				}
				clock.add(3 * time.Second)
				return sample, nil
			})
			r := prepare(t, c)
			got, err := c.Run(context.Background(), r.Ticket, true)
			if err != nil || got.Outcome != "completed" || got.Sample == nil || got.Sample.Replies != replies || got.Sample.Timeouts != 3-replies {
				t.Fatalf("result=%+v error=%v", got, err)
			}
			events := audit.copy()
			if len(events) != 3 || events[0].Measurement != nil || events[1].Measurement != nil ||
				!reflect.DeepEqual(events[2].Measurement, auditMeasurement(got.Sample)) || events[2].SchemaVersion != EventSchemaVersion {
				t.Fatal("measurement not committed with terminal audit")
			}
			if (got.Sample.MeanRTT == nil) != (replies == 0) {
				t.Fatal("invented or missing latency")
			}
			raw, err := json.Marshal(events[2])
			if err != nil || len(raw) > 2048 {
				t.Fatal("unbounded terminal evidence", err)
			}
			if replies == 0 && strings.Contains(string(raw), "mean_rtt_ns") {
				t.Fatal("unknown latency became zero")
			}
			if replies > 0 && !strings.Contains(string(raw), `"mean_rtt_ns":0`) {
				t.Fatal("measured zero disappeared")
			}
			for _, word := range []string{"ticket", "nonce", "payload_bytes", "packet_loss", "internet", "secret"} {
				if strings.Contains(string(raw), word) {
					t.Fatal("unnecessary terminal evidence", word)
				}
			}
		})
	}
}

func TestPartialUnavailableAndCanceledMeasurements(t *testing.T) {
	for _, mode := range []string{"partial", "uncertain-send", "no-socket", "canceled", "deadline", "cleanup-failed", "cancel-after-complete"} {
		t.Run(mode, func(t *testing.T) {
			c, clock, audit, _ := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.deps.Executor = executorFunc(func(_ context.Context, s Selection) (gatewayicmp.Sample, error) {
				sample := completeSample(s, clock.now(), 3)
				sample.Complete = false
				sample.CompletedAt = time.Time{}
				sample.MeanRTT = nil
				sample.SendCalls = 1
				sample.AcceptedRequests = 1
				sample.Replies = 0
				switch mode {
				case "no-socket":
					return gatewayicmp.Sample{}, gatewayicmp.ErrPermission
				case "uncertain-send":
					sample.SendCalls = 2
					sample.Replies = 1
					clock.add(time.Second)
				case "canceled":
					cancel()
					return sample, context.Canceled
				case "deadline":
					return sample, context.DeadlineExceeded
				case "cleanup-failed":
					sample = completeSample(s, clock.now(), 3)
					sample.Complete = false
					sample.MeanRTT = nil
					clock.add(3 * time.Second)
				case "cancel-after-complete":
					sample = completeSample(s, clock.now(), 3)
					clock.add(3 * time.Second)
					cancel()
					return sample, nil
				}
				return sample, errors.New("private socket diagnostic")
			})
			review := prepare(t, c)
			got, err := c.Run(ctx, review.Ticket, true)
			want := "failed"
			if mode == "canceled" || mode == "deadline" || mode == "cancel-after-complete" {
				want = "canceled"
			}
			if err == nil || got.Outcome != want || len(audit.copy()) != 3 || strings.Contains(err.Error(), "private") {
				t.Fatalf("%+v %v", got, err)
			}
			if mode == "no-socket" {
				if got.Sample != nil || audit.copy()[2].Measurement != nil {
					t.Fatal("unavailable became measurement")
				}
			} else {
				if got.Sample == nil || !reflect.DeepEqual(auditMeasurement(got.Sample), audit.copy()[2].Measurement) {
					t.Fatal("lost valid partial evidence")
				}
				if mode != "cancel-after-complete" && (got.Sample.Complete || got.Sample.MeanRTT != nil) {
					t.Fatal("partial became complete or gained latency")
				}
			}
			if _, err := c.Run(context.Background(), review.Ticket, true); !errors.Is(err, ErrReview) {
				t.Fatal("partial result restored approval")
			}
		})
	}
}

func TestInvalidMeasurementsNeverBecomePublishableResults(t *testing.T) {
	edits := map[string]func(*gatewayicmp.Sample){
		"missing":                 func(s *gatewayicmp.Sample) { *s = gatewayicmp.Sample{} },
		"scope":                   func(s *gatewayicmp.Sample) { s.ScopeID = "scope.other" },
		"interface":               func(s *gatewayicmp.Sample) { s.InterfaceName = "en1" },
		"index":                   func(s *gatewayicmp.Sample) { s.InterfaceIndex++ },
		"source":                  func(s *gatewayicmp.Sample) { s.Source = netip.MustParseAddr("192.168.50.24") },
		"target":                  func(s *gatewayicmp.Sample) { s.Target = netip.MustParseAddr("192.168.50.2") },
		"start-before-execute":    func(s *gatewayicmp.Sample) { s.StartedAt = s.StartedAt.Add(-time.Nanosecond) },
		"future-completion":       func(s *gatewayicmp.Sample) { s.CompletedAt = s.CompletedAt.Add(time.Nanosecond) },
		"no-completion":           func(s *gatewayicmp.Sample) { s.CompletedAt = time.Time{} },
		"invalid-time":            func(s *gatewayicmp.Sample) { s.StartedAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) },
		"short-duration":          func(s *gatewayicmp.Sample) { s.CompletedAt = s.StartedAt.Add(time.Second) },
		"negative-count":          func(s *gatewayicmp.Sample) { s.SendCalls = -1 },
		"excess-count":            func(s *gatewayicmp.Sample) { s.SendCalls = math.MaxInt },
		"outcomes-overflow":       func(s *gatewayicmp.Sample) { s.Replies = math.MaxInt; s.Timeouts = math.MaxInt },
		"accepted-more-than-sent": func(s *gatewayicmp.Sample) { s.SendCalls = 2 },
		"missing-outcome":         func(s *gatewayicmp.Sample) { s.Replies = 2 },
		"no-error-incomplete":     func(s *gatewayicmp.Sample) { s.Complete = false; s.MeanRTT = nil },
		"missing-rtt":             func(s *gatewayicmp.Sample) { s.MeanRTT = nil },
		"negative-rtt":            func(s *gatewayicmp.Sample) { v := -time.Nanosecond; s.MeanRTT = &v },
		"rtt-at-attempt-deadline": func(s *gatewayicmp.Sample) { v := time.Second; s.MeanRTT = &v },
		"latency-without-replies": func(s *gatewayicmp.Sample) { s.Replies = 0; s.Timeouts = 3 },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			c, clock, audit, _ := fixture(t)
			c.deps.Executor = executorFunc(func(_ context.Context, s Selection) (gatewayicmp.Sample, error) {
				sample := completeSample(s, clock.now(), 3)
				clock.add(3 * time.Second)
				edit(&sample)
				return sample, nil
			})
			r := prepare(t, c)
			got, err := c.Run(context.Background(), r.Ticket, true)
			if !errors.Is(err, ErrExecution) || got.Outcome != "failed" || got.Sample != nil {
				t.Fatalf("invalid result escaped: %+v %v", got, err)
			}
			events := audit.copy()
			if len(events) != 3 || events[2].Reason != "measurement-invalid" || events[2].Measurement != nil {
				t.Fatal("invalid measurement persisted")
			}
			if _, err := c.Run(context.Background(), r.Ticket, true); !errors.Is(err, ErrReview) {
				t.Fatal("invalid measurement restored approval")
			}
		})
	}
}

func TestOriginalExpiryRejectsNewerPreflightMeasurementAtBoundary(t *testing.T) {
	c, clock, audit, _ := fixture(t)
	r := prepare(t, c)
	clock.add(27 * time.Second)
	c.deps.Executor = executorFunc(func(_ context.Context, s Selection) (gatewayicmp.Sample, error) {
		if !s.Plan.ReviewExpiresAt.After(r.ExpiresAt) {
			t.Fatal("missing newer evidence")
		}
		sample := completeSample(s, clock.now(), 3)
		clock.add(3 * time.Second)
		return sample, nil
	})
	got, err := c.Run(context.Background(), r.Ticket, true)
	if !errors.Is(err, ErrExecution) || got.Sample != nil || audit.copy()[2].Measurement != nil {
		t.Fatal("new plan extended consumed approval")
	}
}

func TestMeasurementPointersDoNotAliasExecutorAuditOrCaller(t *testing.T) {
	c, clock, audit, _ := fixture(t)
	var supplied gatewayicmp.Sample
	c.deps.Executor = executorFunc(func(_ context.Context, s Selection) (gatewayicmp.Sample, error) {
		supplied = completeSample(s, clock.now(), 3)
		clock.add(3 * time.Second)
		return supplied, nil
	})
	audit.hook = func(e Event) {
		if e.State == "finished" {
			*supplied.MeanRTT = 42 * time.Second
		}
	}
	got, err := c.Run(context.Background(), prepare(t, c).Ticket, true)
	if err != nil || got.Sample == nil || *got.Sample.MeanRTT != time.Millisecond {
		t.Fatal("executor pointer retained", err)
	}
	terminal := audit.copy()[2].Measurement
	*got.Sample.MeanRTT = 7 * time.Second
	got.Sample.ScopeID = "scope.changed"
	if *terminal.MeanRTTNanoseconds != int64(time.Millisecond) {
		t.Fatal("caller mutated persistent evidence")
	}
	*terminal.MeanRTTNanoseconds = 99
	if *got.Sample.MeanRTT != 7*time.Second {
		t.Fatal("audit mutated result")
	}
}

func TestVersionedMeasurementAuditValidation(t *testing.T) {
	c, _, audit, _ := fixture(t)
	if _, err := c.Run(context.Background(), prepare(t, c).Ticket, true); err != nil {
		t.Fatal(err)
	}
	complete := audit.copy()[2]
	for _, version := range []int{1, EventSchemaVersion} {
		e := complete
		e.SchemaVersion = version
		e.Measurement = nil
		if (ValidateEvent(e) == nil) != (version == 1) {
			t.Fatal("legacy completion upgraded into measured success")
		}
	}
	for name, edit := range map[string]func(*Event){
		"legacy-with-measurement":  func(e *Event) { e.SchemaVersion = 1 },
		"authorized":               func(e *Event) { e.State = "authorized"; e.Outcome = ""; e.Reason = "" },
		"admitted":                 func(e *Event) { e.State = "admitted"; e.Outcome = ""; e.Reason = "" },
		"blocked":                  func(e *Event) { e.Outcome = "blocked"; e.Reason = "selection-changed" },
		"panic":                    func(e *Event) { e.Outcome = "indeterminate"; e.Reason = "execution-panic" },
		"failed-complete":          func(e *Event) { e.Outcome = "failed"; e.Reason = "execution-error" },
		"invalid-with-measurement": func(e *Event) { e.Outcome = "failed"; e.Reason = "measurement-invalid" },
		"future":                   func(e *Event) { e.At = e.Measurement.StartedAt },
	} {
		t.Run(name, func(t *testing.T) {
			e := complete
			edit(&e)
			if ValidateEvent(e) == nil {
				t.Fatal("contradictory measured audit")
			}
		})
	}
}

func FuzzMeasurementCountsAndTimes(f *testing.F) {
	f.Add(3, 3, 2, 1, int64(3*time.Second), int64(time.Millisecond), true)
	f.Add(1, 0, 0, 0, int64(0), int64(0), false)
	f.Fuzz(func(t *testing.T, sends, accepted, replies, timeouts int, elapsed, mean int64, complete bool) {
		start := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
		end := start.Add(time.Duration(elapsed))
		m := Measurement{StartedAt: start, SendCalls: sends, AcceptedRequests: accepted, Replies: replies, Timeouts: timeouts, Complete: complete}
		if complete {
			m.CompletedAt = &end
			m.MeanRTTNanoseconds = &mean
		}
		if validateMeasurement(m, end) == nil {
			if sends < 0 || sends > 3 || accepted < 0 || accepted > sends || replies < 0 || timeouts < 0 || replies+timeouts > accepted {
				t.Fatal("impossible counts")
			}
		}
	})
}
