package localapi

import (
	"context"
	"errors"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

type ResolverHistoryHandler interface {
	ResolverHistory(context.Context, api.ResolverHistoryParams) (api.ResolverHistory, error)
}

// Authenticated historical read, not an execution session. Only a run reference
// may be supplied; scope and times are resolved by the controller.
func (s *Server) readResolverHistory(parent context.Context, conn net.Conn, request api.Request) (api.ResolverHistory, bool) {
	var params api.ResolverHistoryParams
	if len(request.Params) != 0 {
		if decodeGatewayObject(request.Params, map[string]any{"run_id": &params.RunID}) != nil || !resolverrun.ValidRunID(params.RunID) {
			s.writeError(conn, request.ID, "invalid_request", "history accepts only an optional run reference")
			return api.ResolverHistory{}, false
		}
	}
	handler, ok := s.handler.(ResolverHistoryHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return api.ResolverHistory{}, false
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	result, err := handler.ResolverHistory(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRead):
			s.writeError(conn, request.ID, "invalid_request", "invalid history reference")
		case errors.Is(err, ErrReadTargetNotFound):
			s.writeError(conn, request.ID, "not_found", "retained resolver evidence is not available in the enrolled scope")
		default:
			s.logger.Warn("local_api_request_failed", "method", api.MethodResolverHistory)
			s.writeError(conn, request.ID, "internal_error", "retained resolver history is unavailable")
		}
		return api.ResolverHistory{}, false
	}
	return result, true
}
