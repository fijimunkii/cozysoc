//go:build !darwin

package gatewayicmp

import "context"

func platform() (func(context.Context, Request) (routeEvidence, error), func(context.Context, Request) (socket, error)) {
	return func(ctx context.Context, _ Request) (routeEvidence, error) {
			if err := ctx.Err(); err != nil {
				return routeEvidence{}, err
			}
			return routeEvidence{}, ErrUnsupported
		}, func(ctx context.Context, _ Request) (socket, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, ErrUnsupported
		}
}
