//go:build !darwin

package checkroute

import (
	"context"
	"net/netip"
)

type unsupported struct{}

func NewInspector() Inspector { return unsupported{} }
func (unsupported) Inspect(ctx context.Context, _ Enrollment, _ netip.Addr) (Evidence, error) {
	if ctx.Err() != nil {
		return Evidence{}, ctx.Err()
	}
	return Evidence{}, ErrUnsupported
}
