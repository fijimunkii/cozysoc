package main

import (
	"context"
	"errors"
	"strconv"

	"github.com/fijimunkii/cozysoc/internal/controller/adguard"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

func (h *controllerAPIHandler) ConnectAdGuard(ctx context.Context, params api.AdGuardConnectParams) (api.AdGuardConnection, error) {
	if h.adguardConnections == nil {
		return api.AdGuardConnection{}, errors.New("AdGuard Home connection is unavailable")
	}
	connection, err := h.adguardConnections.Connect(ctx, params.Endpoint, params.Username, secretstore.NewSecret([]byte(params.Password)))
	if err != nil {
		return api.AdGuardConnection{}, err
	}
	return adguardProjection(connection), nil
}

func (h *controllerAPIHandler) AdGuardStatus(ctx context.Context) (api.AdGuardConnection, error) {
	if h.adguardConnections == nil {
		return api.AdGuardConnection{}, errors.New("AdGuard Home connection is unavailable")
	}
	connection, err := h.adguardConnections.Current(ctx)
	if errors.Is(err, adguard.ErrNotConnected) {
		return api.AdGuardConnection{Connected: false}, nil
	}
	if err != nil {
		return api.AdGuardConnection{}, err
	}
	return adguardProjection(connection), nil
}

func (h *controllerAPIHandler) DisconnectAdGuard(ctx context.Context) (api.AdGuardConnection, error) {
	if h.adguardConnections == nil {
		return api.AdGuardConnection{}, errors.New("AdGuard Home connection is unavailable")
	}
	if err := h.adguardConnections.Disconnect(ctx); err != nil {
		return api.AdGuardConnection{}, err
	}
	return api.AdGuardConnection{Connected: false}, nil
}

func adguardProjection(connection adguard.Connection) api.AdGuardConnection {
	status := connection.Status
	result := api.AdGuardConnection{Connected: true, Endpoint: connection.Endpoint, Username: connection.Username, Version: status.Version,
		Running: status.Running, ProtectionEnabled: status.ProtectionEnabled, FilteringEnabled: status.FilteringEnabled,
		QueryLogEnabled: status.QueryLogEnabled, AnonymizedClients: status.AnonymizedClients}
	if inventory := status.FilterInventory; inventory.Available {
		projected := api.AdGuardFilterInventory{BlocklistTotal: inventory.BlocklistTotal, AllowlistTotal: inventory.AllowlistTotal,
			Truncated: inventory.Truncated, Sources: make([]api.AdGuardFilterSource, 0, len(inventory.Sources))}
		for _, source := range inventory.Sources {
			projected.Sources = append(projected.Sources, api.AdGuardFilterSource{Kind: source.Kind, ID: strconv.FormatInt(source.ID, 10),
				Name: source.Name, Enabled: source.Enabled, RulesCount: source.RulesCount, LastUpdated: source.LastUpdated})
		}
		result.FilterInventory = &projected
	}
	return result
}

func (h *controllerAPIHandler) CollectAdGuard(ctx context.Context, params api.AdGuardCollectParams) (api.AdGuardCollection, error) {
	if h.adguardCollector == nil {
		return api.AdGuardCollection{}, errors.New("AdGuard Home collection is unavailable")
	}
	var result adguard.CollectionResult
	var err error
	if params.Expected == nil {
		result, err = h.adguardCollector.Collect(ctx, params.ScopeID)
	} else {
		binding := devicewatch.ScopeBinding{InterfaceName: params.Expected.Interface.InterfaceName,
			InterfaceIndex: params.Expected.Interface.InterfaceIndex, Prefixes: params.Expected.Interface.Prefixes}
		result, err = h.adguardCollector.CollectReviewed(ctx, params.ScopeID, params.Expected.Endpoint, binding)
	}
	if err != nil {
		return api.AdGuardCollection{}, err
	}
	return api.AdGuardCollection{ScopeID: result.ScopeID, QueryLogEnabled: result.QueryLogEnabled,
		Read: result.Read, Inserted: result.Inserted, Deduplicated: result.Deduplicated + result.Skipped.Duplicate,
		SkippedOutsideScope: result.Skipped.OutsideScope, SkippedWithoutClientIP: result.Skipped.WithoutClientIP,
		SkippedOutsideWindow: result.Skipped.OutsideWindow, LimitReached: result.LimitReached}, nil
}
