package localapi

import (
	"context"
	"errors"
	"net"
	"regexp"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

var resolverSelectionID = regexp.MustCompile(`^selection\.[0-9a-f]{32}$`)

func ValidResolverSelectionID(id string) bool { return resolverSelectionID.MatchString(id) }

type ResolverSettingsHandler interface {
	SaveResolver(context.Context, api.ResolverSettingsParams) (api.ResolverSettingsResult, error)
	ListResolvers(context.Context) (api.ResolverSettingsResult, error)
	RetireResolver(context.Context, api.ResolverIDParams) (api.ResolverRetireResult, error)
	PreviewResolver(context.Context, api.ResolverIDParams) (api.ResolverPlan, error)
}

// These narrow native methods run after OS-peer, session-secret and API-version
// checks. Exact JSON fields reject consent, budgets and caller-owned references.
func (s *Server) resolverSettings(ctx context.Context, conn net.Conn, request api.Request) (any, bool) {
	handler, ok := s.handler.(ResolverSettingsHandler)
	if !ok {
		s.writeError(conn, request.ID, "method_not_found", "method is not available")
		return nil, false
	}
	var p api.ResolverSettingsParams
	var id api.ResolverIDParams
	fields := map[string]any{}
	switch request.Method {
	case api.MethodResolverSave:
		fields = map[string]any{"endpoint": &p.Endpoint, "name": &p.Name, "family": &p.Family, "transport": &p.Transport, "query_type": &p.QueryType, "expect": &p.Expect, "destination_scope": &p.DestinationScope}
	case api.MethodResolverRetire, api.MethodResolverPlan:
		fields = map[string]any{"selection_id": &id.SelectionID}
	}
	if decodeGatewayObject(request.Params, fields) != nil ||
		((request.Method == api.MethodResolverRetire || request.Method == api.MethodResolverPlan) && !ValidResolverSelectionID(id.SelectionID)) {
		s.writeError(conn, request.ID, "invalid_request", "resolver method requires exact settings or an opaque selection reference")
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var result any
	var err error
	switch request.Method {
	case api.MethodResolverSave:
		result, err = handler.SaveResolver(ctx, p)
	case api.MethodResolverList:
		result, err = handler.ListResolvers(ctx)
	case api.MethodResolverRetire:
		result, err = handler.RetireResolver(ctx, id)
	case api.MethodResolverPlan:
		result, err = handler.PreviewResolver(ctx, id)
	}
	if err != nil {
		code := "unavailable"
		if errors.Is(err, ErrInvalidRead) {
			code = "invalid_request"
		}
		s.writeError(conn, request.ID, code, "resolver settings or preview unavailable; reload settings before retrying a mutation")
		return nil, false
	}
	return result, true
}
