package localapi

import (
	"context"
	"errors"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
)

type HTTPSHistoryHandler interface {
	HTTPSHistory(context.Context, api.HTTPSHistoryParams) (api.HTTPSHistory, error)
}

// Authenticated historical read, not an execution session. Only a run reference
// may be supplied; scope and times are resolved by the controller.
func (s *Server) readHTTPSHistory(parent context.Context, conn net.Conn, request api.Request) (api.HTTPSHistory, bool) {
	var params api.HTTPSHistoryParams
	if len(request.Params) != 0 {
		if decodeGatewayObject(request.Params, map[string]any{"run_id": &params.RunID}) != nil || !httpsrun.ValidRunID(params.RunID) {
			s.writeError(conn, request.ID, "invalid_request", "history accepts only an optional run reference")
			return api.HTTPSHistory{}, false
		}
	}
	handler, ok := s.handler.(HTTPSHistoryHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return api.HTTPSHistory{}, false
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	result, err := handler.HTTPSHistory(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRead):
			s.writeError(conn, request.ID, "invalid_request", "invalid history reference")
		case errors.Is(err, ErrReadTargetNotFound):
			s.writeError(conn, request.ID, "not_found", "retained https evidence is not available in the enrolled scope")
		default:
			s.logger.Warn("local_api_request_failed", "method", api.MethodHTTPSHistory)
			s.writeError(conn, request.ID, "internal_error", "retained https history is unavailable")
		}
		return api.HTTPSHistory{}, false
	}
	return result, true
}
