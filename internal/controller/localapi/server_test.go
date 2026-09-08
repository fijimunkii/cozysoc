package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type testHandler struct{}

func (testHandler) Status() api.Status {
	return api.Status{APIVersion: api.Version, ControllerVersion: "test", Transport: "unix"}
}

func (testHandler) Health() api.Health {
	return api.Health{State: "ok", LastTickAt: time.Unix(1, 0).UTC()}
}

func TestServerStatusRoundTripAndSocketMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket permission test")
	}
	dir := t.TempDir()
	server, err := NewServer(dir, testHandler{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	info, err := os.Stat(filepath.Join(dir, SocketFilename))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}

	client := NewClient(server.SocketPath())
	result, err := client.Call(context.Background(), api.MethodStatus)
	if err != nil {
		t.Fatal(err)
	}
	var status api.Status
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.ControllerVersion != "test" || status.APIVersion != api.Version {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestSecondServerIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test")
	}
	dir := t.TempDir()
	first, err := NewServer(dir, testHandler{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = first.Serve(ctx) }()

	second, err := NewServer(dir, testHandler{}, nil)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second server error = %v, want ErrAlreadyRunning", err)
	}
}

func TestUnknownMethodReturnsTypedError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test")
	}
	dir := t.TempDir()
	server, err := NewServer(dir, testHandler{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	conn, err := net.Dial("unix", server.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("{\"version\":1,\"id\":\"x\",\"method\":\"mutate\"}\n")); err != nil {
		t.Fatal(err)
	}
	var response api.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "method_not_found" {
		t.Fatalf("unexpected response: %+v", response)
	}
}
