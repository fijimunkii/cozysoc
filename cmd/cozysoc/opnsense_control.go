package main

import (
	"context"
	"errors"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

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
