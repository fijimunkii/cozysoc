package localapi

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type opnsenseNeighborHandler interface {
	OPNsenseNeighbors(context.Context) (api.OPNsenseNeighborHistory, error)
}

func (s *Server) handleOPNsenseNeighbors(ctx context.Context, conn net.Conn, request api.Request) {
	if s.rejectUnexpectedParams(conn, request) {
		return
	}
	handler, ok := s.handler.(opnsenseNeighborHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := handler.OPNsenseNeighbors(requestCtx)
	if err != nil {
		s.writeError(conn, request.ID, "unavailable", "OPNsense neighbor history is unavailable")
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		s.writeError(conn, request.ID, "internal_error", "unable to encode OPNsense neighbor history")
		return
	}
	_ = json.NewEncoder(conn).Encode(api.Response{Version: api.Version, ID: request.ID, Result: encoded})
}
