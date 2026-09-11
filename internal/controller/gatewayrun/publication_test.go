package gatewayrun

import (
	"context"
	"errors"
	"testing"
	"time"
)

type auditorFunc func(context.Context, Event) error

func (f auditorFunc) InsertGatewayRunAudit(ctx context.Context, event Event) error {
	return f(ctx, event)
}

func TestUncertainCommittedTerminalAuditCannotPublishOrRetry(t *testing.T) {
	for _, panicAfterCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "reported-error", true: "panic"}[panicAfterCommit], func(t *testing.T) {
			c, clock, audit, calls := fixture(t)
			c.deps.Auditor = auditorFunc(func(ctx context.Context, event Event) error {
				if err := audit.InsertGatewayRunAudit(ctx, event); err != nil {
					return err
				}
				if event.State == "finished" {
					if panicAfterCommit {
						panic("uncertain commit acknowledgement")
					}
					return errors.New("uncertain commit acknowledgement")
				}
				return nil
			})
			review := prepare(t, c)
			result, err := c.Run(context.Background(), review.Ticket, true)
			if err != ErrAudit || result != (Result{}) || calls.Load() != 1 {
				t.Fatalf("published unconfirmed evidence: %+v %v", result, err)
			}
			events := audit.copy()
			if len(events) != 3 || events[2].Measurement == nil || !events[2].Measurement.Complete {
				t.Fatal("fixture did not commit measured terminal evidence")
			}
			clock.add(time.Hour)
			if _, err := c.Prepare(context.Background(), testTarget); err != ErrAudit {
				t.Fatal("uncertain audit lock cleared", err)
			}
			if _, err := c.Run(context.Background(), review.Ticket, true); err != ErrAudit || calls.Load() != 1 || len(audit.copy()) != 3 {
				t.Fatal("uncertain terminal write was retried", err)
			}
		})
	}
}
