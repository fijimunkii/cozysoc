package localapi

import (
	"context"
	"errors"
	"net"
	"regexp"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

var httpsSelectionID = regexp.MustCompile(`^https-selection\.[0-9a-f]{32}$`)

func ValidHTTPSSelectionID(id string) bool { return httpsSelectionID.MatchString(id) }

type HTTPSSettingsHandler interface {
	SaveHTTPS(context.Context, api.HTTPSSettingsParams) (api.HTTPSSettingsResult, error)
	ListHTTPSSettings(context.Context) (api.HTTPSSettingsResult, error)
	RetireHTTPS(context.Context, api.HTTPSIDParams) (api.HTTPSRetireResult, error)
}

// These narrow native methods run after OS-peer, session-secret and API-version
// checks. Exact JSON fields reject consent, budgets and caller-owned references.
func (s *Server) httpsSettings(ctx context.Context, conn net.Conn, request api.Request) (any, bool) {
	handler, ok := s.handler.(HTTPSSettingsHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return nil, false
	}
	var p api.HTTPSSettingsParams
	var id api.HTTPSIDParams
	fields := map[string]any{}
	switch request.Method {
	case api.MethodHTTPSSave:
		fields = map[string]any{"endpoint": &p.Endpoint, "server_name": &p.ServerName, "request_target": &p.RequestTarget, "family": &p.Family, "method": &p.Method, "expected_status": &p.ExpectedStatus, "destination_policy": &p.DestinationPolicy}
	case api.MethodHTTPSRetire:
		fields = map[string]any{"selection_id": &id.SelectionID}
	}
	if decodeGatewayObject(request.Params, fields) != nil ||
		(request.Method == api.MethodHTTPSRetire && !ValidHTTPSSelectionID(id.SelectionID)) {
		s.writeError(conn, request.ID, "invalid_request", "https method requires exact settings or an opaque selection reference")
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var result any
	var err error
	switch request.Method {
	case api.MethodHTTPSSave:
		result, err = handler.SaveHTTPS(ctx, p)
	case api.MethodHTTPSList:
		result, err = handler.ListHTTPSSettings(ctx)
	case api.MethodHTTPSRetire:
		result, err = handler.RetireHTTPS(ctx, id)
	}
	if err != nil {
		code := "unavailable"
		if errors.Is(err, ErrInvalidRead) {
			code = "invalid_request"
		}
		s.writeError(conn, request.ID, code, "HTTPS settings unavailable; reload settings before retrying a mutation")
		return nil, false
	}
	return result, true
}
