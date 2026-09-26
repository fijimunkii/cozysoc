package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

func (h *controllerAPIHandler) OPNsenseNeighbors(ctx context.Context) (api.OPNsenseNeighborHistory, error) {
	asOf := h.now().UTC()
	result := api.OPNsenseNeighborHistory{AsOf: asOf, Reports: []api.OPNsenseNeighborReport{}}
	scopes, err := h.store.ListActiveDeviceWatchScopes(ctx)
	if err != nil || len(scopes) > 1 {
		return api.OPNsenseNeighborHistory{}, fmt.Errorf("resolve enrolled router observation scope")
	}
	if len(scopes) == 0 {
		return result, nil
	}
	history, err := opnsense.RecentNeighbors(ctx, h.store, scopes[0].ID, asOf)
	if err != nil {
		return api.OPNsenseNeighborHistory{}, err
	}
	result.ScopeEnrolled = true
	result.ScopeID = history.ScopeID
	result.Truncated = history.Truncated
	for _, report := range history.Reports {
		result.Reports = append(result.Reports, api.OPNsenseNeighborReport{ObservationID: report.ObservationID,
			CapturedAt: report.CapturedAt, Address: report.Address, Hardware: report.Hardware,
			Interface: report.Interface, Family: report.Family})
	}
	return result, nil
}

func (h *controllerAPIHandler) ConnectOPNsense(ctx context.Context, params api.OPNsenseConnectParams) (api.OPNsenseConnection, error) {
	if h.opnsenseConnections == nil {
		return api.OPNsenseConnection{}, errors.New("OPNsense connection is unavailable")
	}
	connection, err := h.opnsenseConnections.Connect(ctx, params.Endpoint, params.APIKey, secretstore.NewSecret([]byte(params.APISecret)), []byte(params.TrustPEM))
	if err != nil {
		return api.OPNsenseConnection{}, err
	}
	return api.OPNsenseConnection{Connected: true, Endpoint: connection.Endpoint, Version: connection.Status.Version}, nil
}

func (h *controllerAPIHandler) OPNsenseStatus(ctx context.Context) (api.OPNsenseConnection, error) {
	if h.opnsenseConnections == nil {
		return api.OPNsenseConnection{}, errors.New("OPNsense connection is unavailable")
	}
	connection, err := h.opnsenseConnections.Current(ctx)
	if errors.Is(err, opnsense.ErrNotConnected) {
		return api.OPNsenseConnection{Connected: false}, nil
	}
	if err != nil {
		return api.OPNsenseConnection{}, err
	}
	return api.OPNsenseConnection{Connected: true, Endpoint: connection.Endpoint, Version: connection.Status.Version}, nil
}

func (h *controllerAPIHandler) DisconnectOPNsense(ctx context.Context) (api.OPNsenseConnection, error) {
	if h.opnsenseConnections == nil {
		return api.OPNsenseConnection{}, errors.New("OPNsense connection is unavailable")
	}
	if err := h.opnsenseConnections.Disconnect(ctx); err != nil {
		return api.OPNsenseConnection{}, err
	}
	return api.OPNsenseConnection{Connected: false}, nil
}

func (h *controllerAPIHandler) CollectOPNsense(ctx context.Context, params api.OPNsenseCollectParams) (api.OPNsenseCollection, error) {
	if h.opnsenseCollector == nil {
		return api.OPNsenseCollection{}, errors.New("OPNsense collection is unavailable")
	}
	binding := devicewatch.ScopeBinding{InterfaceName: params.Expected.Interface.InterfaceName,
		InterfaceIndex: params.Expected.Interface.InterfaceIndex, Prefixes: params.Expected.Interface.Prefixes}
	result, err := h.opnsenseCollector.CollectReviewed(ctx, params.ScopeID, params.Expected.Endpoint, binding)
	if err != nil {
		return api.OPNsenseCollection{}, err
	}
	return api.OPNsenseCollection{ScopeID: result.ScopeID, Read: result.Read,
		IPv4Total: result.IPv4Total, IPv6Total: result.IPv6Total,
		IPv4Truncated: result.IPv4Truncated, IPv6Truncated: result.IPv6Truncated,
		Inserted: result.Inserted, Deduplicated: result.Deduplicated,
		SkippedOutside: result.SkippedOutside, SkippedDuplicate: result.SkippedDuplicate}, nil
}
