//go:build !darwin

package gatewayroute

import (
	"context"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"net/netip"
)

type unsupportedInspector struct{}

func NewInspector() Inspector { return unsupportedInspector{} }
func (unsupportedInspector) Inspect(ctx context.Context, _ networkquality.GatewayPlanBinding, _ netip.Addr) (Evidence, error) {
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	return Evidence{}, ErrUnsupported
}
