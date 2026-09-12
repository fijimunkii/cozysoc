// Package httpsroute verifies local route metadata before constructing a review.
// Inspection sends no IP traffic and grants no execution or socket authority.
package httpsroute

import (
	"context"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/checkroute"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
)

var (
	ErrUnsupported = checkroute.ErrUnsupported
	ErrUnavailable = checkroute.ErrUnavailable
	ErrPermission  = checkroute.ErrPermission
	ErrNoRoute     = checkroute.ErrNoRoute
	ErrMismatch    = checkroute.ErrMismatch
)

type Enrollment = checkroute.Enrollment
type Inspector interface {
	Inspect(context.Context, Enrollment, httpsplan.Configuration) (Selection, error)
}
type inspector struct{ metadata checkroute.Inspector }

func NewInspector() Inspector { return inspector{metadata: checkroute.NewInspector()} }

// Selection retains route age independently of the review's creation time.
// It is controller-owned evidence, never a caller-submittable execution ticket.
type Selection struct {
	Plan                             httpsplan.Plan
	RouteObservedAt, RouteFreshUntil time.Time
}

func (i inspector) Inspect(ctx context.Context, e Enrollment, c httpsplan.Configuration) (Selection, error) {
	if ctx.Err() != nil {
		return Selection{}, ctx.Err()
	}
	if httpsplan.ValidateConfiguration(c) != nil {
		return Selection{}, ErrMismatch
	}
	if i.metadata == nil {
		return Selection{}, ErrUnavailable
	}
	r, err := i.metadata.Inspect(ctx, e, c.Endpoint.Addr())
	if err != nil {
		return Selection{}, err
	}
	if ctx.Err() != nil {
		return Selection{}, ctx.Err()
	}
	p, err := httpsplan.New(httpsplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: r.Source}, c, r.CompletedAt)
	if err != nil {
		return Selection{}, ErrMismatch
	}
	return Selection{Plan: p, RouteObservedAt: r.ObservedAt, RouteFreshUntil: r.FreshUntil}, nil
}
