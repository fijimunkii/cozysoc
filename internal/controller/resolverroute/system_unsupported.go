//go:build !darwin

package resolverroute

import (
	"context"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

type unsupported struct{}

func NewInspector() Inspector { return unsupported{} }
func (unsupported) Inspect(ctx context.Context, _ Enrollment, _ resolverplan.Configuration) (resolverrun.Selection, error) {
	if ctx.Err() != nil {
		return resolverrun.Selection{}, ctx.Err()
	}
	return resolverrun.Selection{}, ErrUnsupported
}
