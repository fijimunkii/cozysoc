// Package resolverroute verifies local route metadata before constructing a review.
// Inspection sends no IP traffic and grants no execution or socket authority.
package resolverroute

import (
	"context"

	"github.com/fijimunkii/cozysoc/internal/controller/checkroute"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
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
	Inspect(context.Context, Enrollment, resolverplan.Configuration) (resolverrun.Selection, error)
}
type inspector struct{ metadata checkroute.Inspector }

func NewInspector() Inspector { return inspector{metadata: checkroute.NewInspector()} }
func (i inspector) Inspect(ctx context.Context, e Enrollment, c resolverplan.Configuration) (resolverrun.Selection, error) {
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	if resolverplan.ValidateConfiguration(c) != nil {
		return resolverrun.Selection{}, ErrMismatch
	}
	if i.metadata == nil {
		return resolverrun.Selection{}, ErrUnavailable
	}
	r, err := i.metadata.Inspect(ctx, e, c.Endpoint.Addr())
	if err != nil {
		return resolverrun.Selection{}, err
	}
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	p, err := resolverplan.New(resolverplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: r.Source}, c, r.CompletedAt)
	if err != nil {
		return resolverrun.Selection{}, ErrMismatch
	}
	return resolverrun.Selection{Plan: p, RouteObservedAt: r.ObservedAt, RouteFreshUntil: r.FreshUntil}, nil
}
