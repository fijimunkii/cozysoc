package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type Client struct {
	stateDir string
	socket   string
}

func NewClient(stateDir string) *Client {
	return &Client{
		stateDir: stateDir,
		socket:   filepath.Join(stateDir, SocketFilename),
	}
}

func (c *Client) Call(ctx context.Context, method string) (json.RawMessage, error) {
	secret, err := loadSessionSecret(c.stateDir)
	if err != nil {
		return nil, err
	}

	dialer := net.Dialer{Timeout: requestTimeout}
	conn, err := dialer.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("connect to controller: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))

	request := api.Request{
		Version: api.Version,
		ID:      "cli",
		Method:  method,
		Auth:    secret,
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nil, fmt.Errorf("send controller request: %w", err)
	}

	var response api.Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("read controller response: %w", err)
	}
	if response.Version != api.Version {
		return nil, fmt.Errorf("controller API version mismatch: got %d want %d", response.Version, api.Version)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("controller error %s: %s", response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}
