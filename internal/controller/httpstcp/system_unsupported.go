//go:build !darwin

package httpstcp

import (
	"context"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
)

func openSystem(ctx context.Context, _ httpsroute.Selection) (net.Conn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, ErrUnsupported
}
