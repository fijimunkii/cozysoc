package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func (c *Client) ConnectOPNsense(ctx context.Context, params api.OPNsenseConnectParams) (api.OPNsenseConnection, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return api.OPNsenseConnection{}, fmt.Errorf("encode OPNsense connection request: %w", err)
	}
	return c.opnsenseCall(ctx, api.MethodOPNsenseConnect, encoded)
}

func (c *Client) OPNsenseStatus(ctx context.Context) (api.OPNsenseConnection, error) {
	return c.opnsenseCall(ctx, api.MethodOPNsenseStatus, nil)
}

func (c *Client) OPNsenseNeighbors(ctx context.Context) (api.OPNsenseNeighborHistory, error) {
	raw, err := c.callWithTimeout(ctx, api.MethodOPNsenseNeighbors, nil, 5*time.Second)
	if err != nil {
		return api.OPNsenseNeighborHistory{}, err
	}
	var result api.OPNsenseNeighborHistory
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.OPNsenseNeighborHistory{}, fmt.Errorf("decode OPNsense neighbor history: %w", err)
	}
	return result, nil
}

func (c *Client) DisconnectOPNsense(ctx context.Context) (api.OPNsenseConnection, error) {
	return c.opnsenseCall(ctx, api.MethodOPNsenseDisconnect, nil)
}

func (c *Client) CollectOPNsense(ctx context.Context, params api.OPNsenseCollectParams) (api.OPNsenseCollection, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return api.OPNsenseCollection{}, fmt.Errorf("encode OPNsense collection request: %w", err)
	}
	raw, err := c.callWithTimeout(ctx, api.MethodOPNsenseCollect, encoded, opnsenseCollectTimeout)
	if err != nil {
		return api.OPNsenseCollection{}, err
	}
	var result api.OPNsenseCollection
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.OPNsenseCollection{}, fmt.Errorf("decode OPNsense collection response: %w", err)
	}
	return result, nil
}

func (c *Client) opnsenseCall(ctx context.Context, method string, params json.RawMessage) (api.OPNsenseConnection, error) {
	raw, err := c.callWithTimeout(ctx, method, params, opnsenseRequestTimeout)
	if err != nil {
		return api.OPNsenseConnection{}, err
	}
	var result api.OPNsenseConnection
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.OPNsenseConnection{}, fmt.Errorf("decode OPNsense connection response: %w", err)
	}
	return result, nil
}
