package localapi

import (
	"context"
	"encoding/json"
	"fmt"

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

func (c *Client) DisconnectOPNsense(ctx context.Context) (api.OPNsenseConnection, error) {
	return c.opnsenseCall(ctx, api.MethodOPNsenseDisconnect, nil)
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
