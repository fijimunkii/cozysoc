package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type storageOverviewTestHandler struct {
	mutationTestHandler
	result api.StorageOverview
	err    error
}

func (h *storageOverviewTestHandler) StorageOverview(context.Context) (api.StorageOverview, error) {
	return h.result, h.err
}

func TestStorageOverviewAuthenticatedRead(t *testing.T) {
	handler := &storageOverviewTestHandler{result: api.StorageOverview{AsOf: time.Unix(1_800_000_000, 0).UTC(), MaxBytes: 1024, Retention: []api.StorageRetention{{Class: "short", DurationSeconds: 86400}}}}
	server := startMutationTestServer(t, handler)
	raw, err := NewClient(server.stateDir).Call(context.Background(), api.MethodStorageOverview)
	if err != nil {
		t.Fatal(err)
	}
	var got api.StorageOverview
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.MaxBytes != 1024 || len(got.Retention) != 1 {
		t.Fatalf("storage overview = %+v", got)
	}
	_, err = NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodStorageOverview, map[string]any{"path": "private"})
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("unexpected params error = %v", err)
	}
}

func TestStorageOverviewHidesControllerError(t *testing.T) {
	server := startMutationTestServer(t, &storageOverviewTestHandler{err: errors.New("private database path")})
	_, err := NewClient(server.stateDir).Call(context.Background(), api.MethodStorageOverview)
	if err == nil || !strings.Contains(err.Error(), "internal_error") || strings.Contains(err.Error(), "private database path") {
		t.Fatalf("storage overview error = %v", err)
	}
}
