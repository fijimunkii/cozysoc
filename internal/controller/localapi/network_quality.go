package localapi

import (
	"context"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type LocalNetworkQualityHandler interface {
	LocalNetworkQuality(context.Context) (api.LocalNetworkQuality, error)
}

// Called only after handleConn's peer, session, and API-version checks.
func (s *Server) readLocalNetworkQuality(conn net.Conn, request api.Request) (api.LocalNetworkQuality, bool) {
	if s.rejectUnexpectedParams(conn, request) {
		return api.LocalNetworkQuality{}, false
	}
	handler, ok := s.handler.(LocalNetworkQualityHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return api.LocalNetworkQuality{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	result, err := handler.LocalNetworkQuality(ctx)
	if err != nil {
		s.logger.Warn("local_api_request_failed", "method", api.MethodNetworkQualityLocal)
		s.writeError(conn, request.ID, "internal_error", "unable to read local network quality")
		return api.LocalNetworkQuality{}, false
	}
	return result, true
}
