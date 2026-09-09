package localapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type mutationTestHandler struct {
	labelErr error
}

func (*mutationTestHandler) Status() api.Status {
	return api.Status{APIVersion: api.Version, ControllerVersion: "test", Transport: "unix"}
}

func (*mutationTestHandler) Health() api.Health {
	return api.Health{State: "ok", LastTickAt: time.Unix(1, 0).UTC()}
}

func (*mutationTestHandler) Capabilities() api.CapabilityList {
	return api.CapabilityList{CatalogSchemaVersion: 1}
}

func (*mutationTestHandler) LabelDevice(_ context.Context, params api.DeviceLabelParams) (api.DeviceLabelResult, error) {
	return api.DeviceLabelResult{DeviceID: params.DeviceID, UserLabel: params.Label, Changed: true}, nil
}

func startMutationTestServer(t *testing.T, handler Handler) *Server {
	t.Helper()
	server, err := newServer(t.TempDir(), handler, slog.New(slog.NewTextHandler(io.Discard, nil)), verifyPeer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return server
}

func TestDeviceLabelRoundTrip(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	result, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodDeviceLabel, api.DeviceLabelParams{
		DeviceID: "device.one",
		Label:    "Living Room TV",
	})
	if err != nil {
		t.Fatal(err)
	}
	var label api.DeviceLabelResult
	if err := json.Unmarshal(result, &label); err != nil {
		t.Fatal(err)
	}
	if label.DeviceID != "device.one" || label.UserLabel != "Living Room TV" || !label.Changed {
		t.Fatalf("unexpected label result: %+v", label)
	}
}

func TestReadMethodsRejectUnexpectedParams(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodStatus, map[string]string{"ignored": "no"})
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("read params error = %v", err)
	}
}

func TestDeviceLabelRejectsMissingAndUnknownParams(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	client := NewClient(server.stateDir)
	for name, params := range map[string]any{
		"missing": nil,
		"unknown": map[string]any{"device_id": "device.one", "label": "TV", "extra": true},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if params == nil {
				_, err = client.Call(context.Background(), api.MethodDeviceLabel)
			} else {
				_, err = client.CallWithParams(context.Background(), api.MethodDeviceLabel, params)
			}
			if err == nil || !strings.Contains(err.Error(), "invalid_request") {
				t.Fatalf("params error = %v", err)
			}
		})
	}
}

func TestRequestRejectsUnknownTopLevelFields(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	secret, err := loadSessionSecret(server.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", server.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	payload := `{"version":1,"id":"x","method":"status","auth":"` + secret + `","surprise":true}` + "\n"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	var response api.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "invalid_request" {
		t.Fatalf("unknown top-level field was accepted: %+v", response)
	}
}
