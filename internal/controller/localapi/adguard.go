package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

const adguardRequestTimeout = 20 * time.Second

type adguardHandler interface {
	ConnectAdGuard(context.Context, api.AdGuardConnectParams) (api.AdGuardConnection, error)
	AdGuardStatus(context.Context) (api.AdGuardConnection, error)
	DisconnectAdGuard(context.Context) (api.AdGuardConnection, error)
}

func (s *Server) handleAdGuard(ctx context.Context, conn net.Conn, request api.Request) {
	handler, ok := s.handler.(adguardHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return
	}
	var params api.AdGuardConnectParams
	if request.Method == api.MethodAdGuardConnect {
		if err := decodeRequiredParams(request.Params, &params); err != nil || params.Endpoint == "" || (params.Username == "") != (params.Password == "") {
			s.writeError(conn, request.ID, "invalid_request", "invalid AdGuard Home connection parameters")
			return
		}
	} else if s.rejectUnexpectedParams(conn, request) {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(adguardRequestTimeout))
	requestCtx, cancel := context.WithTimeout(ctx, adguardRequestTimeout)
	defer cancel()
	var result api.AdGuardConnection
	var err error
	switch request.Method {
	case api.MethodAdGuardConnect:
		result, err = handler.ConnectAdGuard(requestCtx, params)
	case api.MethodAdGuardStatus:
		result, err = handler.AdGuardStatus(requestCtx)
	case api.MethodAdGuardDisconnect:
		result, err = handler.DisconnectAdGuard(requestCtx)
	}
	if err != nil {
		s.writeAdGuardError(conn, request.ID, err)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		s.writeError(conn, request.ID, "internal_error", "unable to encode AdGuard Home result")
		return
	}
	_ = json.NewEncoder(conn).Encode(api.Response{Version: api.Version, ID: request.ID, Result: encoded})
}

func (s *Server) writeAdGuardError(conn net.Conn, id string, err error) {
	switch {
	case errors.Is(err, adguard.ErrEndpoint):
		s.writeError(conn, id, "invalid_request", "AdGuard Home endpoint is not allowed")
	case errors.Is(err, adguard.ErrAlreadyConnected):
		s.writeError(conn, id, "conflict", "an AdGuard Home connection already exists")
	case errors.Is(err, adguard.ErrAuth):
		s.writeError(conn, id, "precondition_failed", "AdGuard Home rejected the credential")
	case errors.Is(err, adguard.ErrVersion):
		s.writeError(conn, id, "precondition_failed", "AdGuard Home version is unsupported")
	case errors.Is(err, adguard.ErrNotRunning):
		s.writeError(conn, id, "precondition_failed", "AdGuard Home is not running")
	case errors.Is(err, secretstore.ErrLocked), errors.Is(err, secretstore.ErrAccessDenied), errors.Is(err, secretstore.ErrUnavailable), errors.Is(err, secretstore.ErrUnsupported), errors.Is(err, secretstore.ErrNotFound):
		s.writeError(conn, id, "precondition_failed", "protected credential storage is unavailable")
	case errors.Is(err, adguard.ErrUnavailable), errors.Is(err, adguard.ErrResponse):
		s.writeError(conn, id, "unavailable", "AdGuard Home did not return a usable status")
	default:
		s.logger.Warn("local_api_request_failed", "method", "adguard")
		s.writeError(conn, id, "internal_error", "unable to update or read AdGuard Home connection")
	}
}
