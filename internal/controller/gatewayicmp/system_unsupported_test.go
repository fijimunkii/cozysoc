//go:build !darwin

package gatewayicmp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnsupportedCandidateDoesNotOpenSockets(t *testing.T) {
	s := NewCandidate()
	r := requestAt(time.Now())
	got, err := s.Measure(context.Background(), r)
	if !errors.Is(err, ErrUnsupported) || got.SendCalls != 0 || got.Complete {
		t.Fatalf("%+v %v", got, err)
	}
	_, open := platform()
	if _, err := open(context.Background(), r); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := open(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
