//go:build !darwin

package resolverudp

import (
	"context"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

func platform() (func(context.Context, resolverrun.Request) (resolverrun.Selection, error), func(context.Context, resolverrun.Request, uint16) (socket, error)) {
	return func(context.Context, resolverrun.Request) (resolverrun.Selection, error) {
		return resolverrun.Selection{}, ErrUnsupported
	}, func(context.Context, resolverrun.Request, uint16) (socket, error) { return nil, ErrUnsupported }
}
