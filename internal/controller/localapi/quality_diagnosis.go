package localapi

import (
	"context"
	"errors"
	"net"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type QualityDiagnosisHandler interface {
	QualityDiagnosis(context.Context) (api.QualityDiagnosis, error)
}

func (s *Server) readQualityDiagnosis(parent context.Context, conn net.Conn, request api.Request) (api.QualityDiagnosis, bool) {
	if len(request.Params) != 0 {
		s.writeError(conn, request.ID, "invalid_request", "quality diagnosis accepts no parameters")
		return api.QualityDiagnosis{}, false
	}
	handler, ok := s.handler.(QualityDiagnosisHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return api.QualityDiagnosis{}, false
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	result, err := handler.QualityDiagnosis(ctx)
	if err != nil {
		if errors.Is(err, ErrReadTargetNotFound) {
			s.writeError(conn, request.ID, "not_found", "historical quality evidence is not available in the enrolled scope")
		} else {
			s.logger.Warn("local_api_request_failed", "method", api.MethodQualityDiagnosis)
			s.writeError(conn, request.ID, "internal_error", "historical quality diagnosis is unavailable")
		}
		return api.QualityDiagnosis{}, false
	}
	return result, true
}
