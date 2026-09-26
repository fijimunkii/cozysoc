package localapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func (c *Client) ConnectAdGuard(ctx context.Context, params api.AdGuardConnectParams) (api.AdGuardConnection, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return api.AdGuardConnection{}, fmt.Errorf("encode AdGuard Home connection request: %w", err)
	}
	return c.adguardCall(ctx, api.MethodAdGuardConnect, encoded)
}

func (c *Client) AdGuardStatus(ctx context.Context) (api.AdGuardConnection, error) {
	return c.adguardCall(ctx, api.MethodAdGuardStatus, nil)
}

func (c *Client) DisconnectAdGuard(ctx context.Context) (api.AdGuardConnection, error) {
	return c.adguardCall(ctx, api.MethodAdGuardDisconnect, nil)
}

func (c *Client) CollectAdGuard(ctx context.Context, scopeID string) (api.AdGuardCollection, error) {
	encoded, err := json.Marshal(api.AdGuardCollectParams{ScopeID: scopeID})
	if err != nil {
		return api.AdGuardCollection{}, fmt.Errorf("encode AdGuard Home collection request: %w", err)
	}
	raw, err := c.callWithTimeout(ctx, api.MethodAdGuardCollect, encoded, adguardCollectTimeout)
	if err != nil {
		return api.AdGuardCollection{}, err
	}
	var result api.AdGuardCollection
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.AdGuardCollection{}, fmt.Errorf("decode AdGuard Home collection result: %w", err)
	}
	return result, nil
}

func (c *Client) adguardCall(ctx context.Context, method string, params json.RawMessage) (api.AdGuardConnection, error) {
	raw, err := c.callWithTimeout(ctx, method, params, adguardRequestTimeout)
	if err != nil {
		return api.AdGuardConnection{}, err
	}
	var result api.AdGuardConnection
	if err := json.Unmarshal(raw, &result); err != nil {
		return api.AdGuardConnection{}, fmt.Errorf("decode AdGuard Home connection response: %w", err)
	}
	return result, nil
}
