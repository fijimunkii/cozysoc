package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

const opnsenseRequestTimeout = 20 * time.Second

type opnsenseHandler interface {
	ConnectOPNsense(context.Context, api.OPNsenseConnectParams) (api.OPNsenseConnection, error)
	OPNsenseStatus(context.Context) (api.OPNsenseConnection, error)
	DisconnectOPNsense(context.Context) (api.OPNsenseConnection, error)
}

func (s *Server) handleOPNsense(ctx context.Context, conn net.Conn, request api.Request) {
	handler, ok := s.handler.(opnsenseHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return
	}
	var params api.OPNsenseConnectParams
	if request.Method == api.MethodOPNsenseConnect {
		if err := decodeRequiredParams(request.Params, &params); err != nil || params.Endpoint == "" || params.APIKey == "" || params.APISecret == "" {
			s.writeError(conn, request.ID, "invalid_request", "invalid OPNsense connection parameters")
			return
		}
	} else if s.rejectUnexpectedParams(conn, request) {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(opnsenseRequestTimeout))
	requestCtx, cancel := context.WithTimeout(ctx, opnsenseRequestTimeout)
	defer cancel()
	var result api.OPNsenseConnection
	var err error
	switch request.Method {
	case api.MethodOPNsenseConnect:
		result, err = handler.ConnectOPNsense(requestCtx, params)
	case api.MethodOPNsenseStatus:
		result, err = handler.OPNsenseStatus(requestCtx)
	case api.MethodOPNsenseDisconnect:
		result, err = handler.DisconnectOPNsense(requestCtx)
	}
	if err != nil {
		s.writeOPNsenseError(conn, request.ID, err)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		s.writeError(conn, request.ID, "internal_error", "unable to encode OPNsense result")
		return
	}
	_ = json.NewEncoder(conn).Encode(api.Response{Version: api.Version, ID: request.ID, Result: encoded})
}

func (s *Server) writeOPNsenseError(conn net.Conn, id string, err error) {
	switch {
	case errors.Is(err, opnsense.ErrEndpoint), errors.Is(err, opnsense.ErrTrust):
		s.writeError(conn, id, "invalid_request", "OPNsense endpoint or certificate is not allowed")
	case errors.Is(err, opnsense.ErrAlreadyConnected):
		s.writeError(conn, id, "conflict", "an OPNsense connection already exists")
	case errors.Is(err, opnsense.ErrAuth):
		s.writeError(conn, id, "precondition_failed", "OPNsense rejected the credential or permission")
	case errors.Is(err, opnsense.ErrVersion):
		s.writeError(conn, id, "precondition_failed", "OPNsense version is unsupported")
	case errors.Is(err, opnsense.ErrNotConnected):
		s.writeError(conn, id, "precondition_failed", "OPNsense is not connected")
	case errors.Is(err, secretstore.ErrLocked), errors.Is(err, secretstore.ErrAccessDenied), errors.Is(err, secretstore.ErrUnavailable), errors.Is(err, secretstore.ErrUnsupported), errors.Is(err, secretstore.ErrNotFound):
		s.writeError(conn, id, "precondition_failed", "protected credential storage is unavailable")
	case errors.Is(err, opnsense.ErrUnavailable), errors.Is(err, opnsense.ErrResponse):
		s.writeError(conn, id, "unavailable", "OPNsense did not return a usable status")
	default:
		s.logger.Warn("local_api_request_failed", "method", "opnsense")
		s.writeError(conn, id, "internal_error", "unable to update or read OPNsense connection")
	}
}
