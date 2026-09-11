package localapi

import (
	"context"
	"errors"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
)

type GatewayHistoryHandler interface {
	GatewayHistory(context.Context, api.GatewayHistoryParams) (api.GatewayHistory, error)
}

// Authenticated historical read, not an execution session. Only a run reference
// may be supplied; scope and times are resolved by the controller.
func (s *Server) readGatewayHistory(parent context.Context, conn net.Conn, request api.Request) (api.GatewayHistory, bool) {
	var params api.GatewayHistoryParams
	if len(request.Params) != 0 {
		if decodeGatewayObject(request.Params, map[string]any{"run_id": &params.RunID}) != nil || !gatewayrun.ValidRunID(params.RunID) {
			s.writeError(conn, request.ID, "invalid_request", "history accepts only an optional run reference")
			return api.GatewayHistory{}, false
		}
	}
	handler, ok := s.handler.(GatewayHistoryHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return api.GatewayHistory{}, false
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	result, err := handler.GatewayHistory(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRead):
			s.writeError(conn, request.ID, "invalid_request", "invalid history reference")
		case errors.Is(err, ErrReadTargetNotFound):
			s.writeError(conn, request.ID, "not_found", "retained gateway evidence is not available in the enrolled scope")
		default:
			s.logger.Warn("local_api_request_failed", "method", api.MethodGatewayHistory)
			s.writeError(conn, request.ID, "internal_error", "retained gateway history is unavailable")
		}
		return api.GatewayHistory{}, false
	}
	return result, true
}
