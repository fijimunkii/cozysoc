package gatewayrun

import (
	"context"
	"testing"
)

func TestDiscardRevokesOnlyItsPendingReview(t *testing.T) {
	c, _, audit, calls := fixture(t)
	first := prepare(t, c)
	c.Discard(Ticket{})
	if _, err := c.Prepare(context.Background(), testTarget); err != ErrBusy {
		t.Fatal("wrong ticket discarded review", err)
	}
	c.Discard(first.Ticket)
	second := prepare(t, c)
	c.Discard(first.Ticket)
	if _, err := c.Run(context.Background(), first.Ticket, true); err != ErrReview {
		t.Fatal("discarded review replayed", err)
	}
	if calls.Load() != 0 || len(audit.copy()) != 0 {
		t.Fatal("discard granted consent")
	}
	if _, err := c.Run(context.Background(), second.Ticket, true); err != nil {
		t.Fatal("stale discard revoked new review", err)
	}
	c.Discard(second.Ticket)
	if _, err := c.Prepare(context.Background(), testTarget); err != ErrCooldown {
		t.Fatal("discard reset run cooldown", err)
	}
	c.Close()
	c.Discard(second.Ticket)
}
