package localapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestDeviceSplitRoundTripAndValidation(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	client := NewClient(server.stateDir)
	response, err := client.CallWithParams(context.Background(), api.MethodDeviceSplit, api.DeviceSplitParams{SourceDeviceID: "device.source", ObservationID: "obs.one"})
	if err != nil {
		t.Fatal(err)
	}
	var correction api.DeviceSplitResult
	if err := json.Unmarshal(response, &correction); err != nil || !correction.Changed || correction.TargetDeviceID != "device.created" || correction.ObservationID != "obs.one" {
		t.Fatal("split response", correction, err)
	}
	response, err = client.CallWithParams(context.Background(), api.MethodDeviceUnsplit, api.DeviceUnsplitParams{ObservationID: "obs.one"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response, &correction); err != nil || !correction.Changed || correction.ObservationID != "obs.one" {
		t.Fatal("unsplit response", correction, err)
	}
	response, err = client.Call(context.Background(), api.MethodDeviceSplits)
	if err != nil {
		t.Fatal(err)
	}
	var list api.DeviceSplitList
	if err := json.Unmarshal(response, &list); err != nil || !list.Configured || list.ScopeID != "scope.home" {
		t.Fatal("split listing", list, err)
	}
	for method, params := range map[string]any{
		api.MethodDeviceSplit:   map[string]any{"source_device_id": "device.source", "observation_id": "obs.one", "scope_id": "scope.other"},
		api.MethodDeviceUnsplit: map[string]any{"observation_id": "obs.one", "scope_id": "scope.other"},
		api.MethodDeviceSplits:  map[string]any{"scope_id": "scope.other"},
	} {
		if _, err := client.CallWithParams(context.Background(), method, params); err == nil || !strings.Contains(err.Error(), "invalid_request") {
			t.Fatal("scope injection", method, err)
		}
	}
}
