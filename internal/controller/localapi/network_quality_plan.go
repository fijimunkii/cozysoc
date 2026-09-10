package localapi

import (
	"context"
	"errors"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

var ErrGatewayPlanPrecondition = errors.New("gateway preview requires a currently verifiable, administratively up enrolled binding")

type GatewayPlanHandler interface {
	PreviewGatewayCheck(context.Context, api.GatewayPlanParams) (api.GatewayCheckPlan, error)
}

// Called only after the shared peer/session/version checks. This read accepts a
// target, never a caller-selected scope, interface, traffic budget, or consent.
func (s *Server) readGatewayPlan(conn net.Conn, request api.Request) (api.GatewayCheckPlan, bool) {
	var params api.GatewayPlanParams
	if err := decodeRequiredParams(request.Params, &params); err != nil || params.Target == "" || len(params.Target) > 15 {
		s.writeError(conn, request.ID, "invalid_request", "gateway preview requires one numeric private IPv4 target")
		return api.GatewayCheckPlan{}, false
	}
	handler, ok := s.handler.(GatewayPlanHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return api.GatewayCheckPlan{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	result, err := handler.PreviewGatewayCheck(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRead):
			s.writeError(conn, request.ID, "invalid_request", "target is not an eligible private IPv4 host in the enrolled prefixes")
		case errors.Is(err, ErrReadTargetNotFound):
			s.writeError(conn, request.ID, "not_found", "enroll a network before reviewing a gateway check")
		case errors.Is(err, ErrGatewayPlanPrecondition):
			s.writeError(conn, request.ID, "precondition_failed", "review the enrolled interface and local connection before requesting a preview")
		default:
			s.logger.Warn("local_api_request_failed", "method", api.MethodNetworkQualityGatewayPlan)
			s.writeError(conn, request.ID, "internal_error", "gateway preview is unavailable")
		}
		return api.GatewayCheckPlan{}, false
	}
	return result, true
}
