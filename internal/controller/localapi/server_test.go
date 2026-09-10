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
	"strings"
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

func (testHandler) Capabilities() api.CapabilityList {
	return api.CapabilityList{CatalogSchemaVersion: 1}
}

func (testHandler) DeviceDetail(_ context.Context, params api.DeviceDetailParams) (api.DeviceDetail, error) {
	if params.DeviceID != "device.one" {
		return api.DeviceDetail{}, ErrReadTargetNotFound
	}
	return api.DeviceDetail{ScopeID: "scope.home", AsOf: time.Unix(2, 0).UTC(), Device: api.DevicePresence{ID: "device.one", FirstSeen: time.Unix(1, 0).UTC(), LastSeen: time.Unix(2, 0).UTC(), State: "visible"}, Evidence: []api.DeviceIdentityEvidence{}}, nil
}

func (testHandler) Devices(context.Context) (api.DeviceList, error) {
	return api.DeviceList{
		Configured: true,
		ScopeID:    "scope.home",
		AsOf:       time.Unix(2, 0).UTC(),
		Devices: []api.DevicePresence{
			{ID: "device.one", FirstSeen: time.Unix(1, 0).UTC(), LastSeen: time.Unix(2, 0).UTC(), State: "visible"},
		},
	}, nil
}

func startTestServer(t *testing.T, verifier peerVerifier) (*Server, context.CancelFunc) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test")
	}
	if verifier == nil {
		verifier = verifyPeer
	}
	server, err := newServer(t.TempDir(), testHandler{}, slog.New(slog.NewTextHandler(io.Discard, nil)), verifier)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return server, cancel
}

func TestServerStatusRoundTripAndPrivateFiles(t *testing.T) {
	server, _ := startTestServer(t, nil)

	for path, want := range map[string]os.FileMode{
		server.SocketPath():                          0o600,
		filepath.Join(server.stateDir, AuthFilename): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", path, got, want)
		}
	}

	client := NewClient(server.stateDir)
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

func TestCapabilitiesListRoundTrip(t *testing.T) {
	server, _ := startTestServer(t, nil)
	client := NewClient(server.stateDir)
	result, err := client.Call(context.Background(), api.MethodCapabilitiesList)
	if err != nil {
		t.Fatal(err)
	}
	var list api.CapabilityList
	if err := json.Unmarshal(result, &list); err != nil {
		t.Fatal(err)
	}
	if list.CatalogSchemaVersion != 1 {
		t.Fatalf("unexpected capability list: %+v", list)
	}
}

func TestDevicesListRoundTrip(t *testing.T) {
	server, _ := startTestServer(t, nil)
	client := NewClient(server.stateDir)
	result, err := client.Call(context.Background(), api.MethodDevicesList)
	if err != nil {
		t.Fatal(err)
	}
	var list api.DeviceList
	if err := json.Unmarshal(result, &list); err != nil {
		t.Fatal(err)
	}
	if !list.Configured || list.ScopeID != "scope.home" || len(list.Devices) != 1 || list.Devices[0].State != "visible" {
		t.Fatalf("unexpected device list: %+v", list)
	}
}

func TestDeviceDetailRoundTripAndTypedNotFound(t *testing.T) {
	server, _ := startTestServer(t, nil)
	client := NewClient(server.stateDir)
	result, err := client.CallWithParams(context.Background(), api.MethodDeviceDetail, api.DeviceDetailParams{DeviceID: "device.one"})
	if err != nil {
		t.Fatal(err)
	}
	var detail api.DeviceDetail
	if err := json.Unmarshal(result, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ScopeID != "scope.home" || detail.Device.ID != "device.one" {
		t.Fatalf("unexpected device detail: %+v", detail)
	}
	_, err = client.CallWithParams(context.Background(), api.MethodDeviceDetail, api.DeviceDetailParams{DeviceID: "device.missing"})
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != "not_found" {
		t.Fatalf("missing device detail error = %v", err)
	}
}

func TestSessionSecretRotatesBetweenServerInstances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test")
	}
	dir := t.TempDir()
	first, err := NewServer(dir, testHandler{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstSecret, err := loadSessionSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := NewServer(dir, testHandler{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondSecret, err := loadSessionSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if firstSecret == secondSecret {
		t.Fatal("controller session secret did not rotate")
	}
}

func TestSecondServerDoesNotRotateActiveSecret(t *testing.T) {
	server, _ := startTestServer(t, nil)
	before, err := loadSessionSecret(server.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewServer(server.stateDir, testHandler{}, nil)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second server error = %v, want ErrAlreadyRunning", err)
	}
	after, err := loadSessionSecret(server.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("failed second server rotated the active controller secret")
	}
}

func TestMissingAndWrongSecretsAreRejected(t *testing.T) {
	server, _ := startTestServer(t, func(net.Conn) (PeerIdentity, error) {
		return PeerIdentity{UID: os.Geteuid(), Verified: true}, nil
	})

	for _, auth := range []string{"", "wrong-secret"} {
		conn, err := net.Dial("unix", server.SocketPath())
		if err != nil {
			t.Fatal(err)
		}
		request := api.Request{Version: api.Version, ID: "x", Method: api.MethodStatus, Auth: auth}
		if err := json.NewEncoder(conn).Encode(request); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		var response api.Response
		if err := json.NewDecoder(conn).Decode(&response); err != nil {
			_ = conn.Close()
			t.Fatal(err)
		}
		_ = conn.Close()
		if response.Error == nil || response.Error.Code != "unauthorized" {
			t.Fatalf("auth %q unexpectedly accepted: %+v", auth, response)
		}
	}
}

func TestWrongPeerUIDIsRejectedBeforeRequestAuthorization(t *testing.T) {
	server, _ := startTestServer(t, func(net.Conn) (PeerIdentity, error) {
		return PeerIdentity{UID: os.Geteuid() + 1, Verified: true}, nil
	})

	conn, err := net.Dial("unix", server.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var response api.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "unauthorized" {
		t.Fatalf("wrong peer UID unexpectedly accepted: %+v", response)
	}
}

func TestNonSocketPathIsNeverRemoved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, SocketFilename)
	if err := os.WriteFile(path, []byte("keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	server, err := NewServer(dir, testHandler{}, nil)
	if server != nil {
		_ = server.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("expected non-socket refusal, got %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "keep-me\n" {
		t.Fatalf("non-socket path was modified: %q", got)
	}
}

func TestUnknownMethodReturnsTypedErrorAfterAuthentication(t *testing.T) {
	server, _ := startTestServer(t, nil)
	secret, err := loadSessionSecret(server.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", server.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := api.Request{Version: api.Version, ID: "x", Method: "mutate", Auth: secret}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
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
