//go:build !darwin

package resolverudp

import (
	"testing"
	"time"
)

func TestUnsupportedSenderNeverOpens(t *testing.T) {
	_, _, r, now := fixture(t)
	s := NewCandidate()
	s.now = func() time.Time { return *now }
	m, e := s.ExecuteResolver(deadline(t), r)
	if e != ErrUnsupported || m.Request != "not-sent" {
		t.Fatalf("%+v %v", m, e)
	}
}
