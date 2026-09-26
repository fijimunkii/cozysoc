package localapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestDeviceMergeAndUnmergeRoundTrip(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	client := NewClient(server.stateDir)
	merge, err := client.CallWithParams(context.Background(), api.MethodDeviceMerge,
		api.DeviceMergeParams{SourceDeviceID: "device.source", TargetDeviceID: "device.target"})
	if err != nil {
		t.Fatal(err)
	}
	var merged api.DeviceIdentityCorrectionResult
	if err := json.Unmarshal(merge, &merged); err != nil || !merged.Changed || merged.SourceDeviceID != "device.source" || merged.TargetDeviceID != "device.target" {
		t.Fatal("merge result", merged, err)
	}
	unmerge, err := client.CallWithParams(context.Background(), api.MethodDeviceUnmerge,
		api.DeviceUnmergeParams{SourceDeviceID: "device.source"})
	if err != nil {
		t.Fatal(err)
	}
	var restored api.DeviceIdentityCorrectionResult
	if err := json.Unmarshal(unmerge, &restored); err != nil || !restored.Changed || restored.SourceDeviceID != "device.source" || restored.TargetDeviceID != "" {
		t.Fatal("unmerge result", restored, err)
	}
	listed, err := client.Call(context.Background(), api.MethodDeviceMerges)
	if err != nil {
		t.Fatal(err)
	}
	var merges api.DeviceMergeList
	if err := json.Unmarshal(listed, &merges); err != nil || !merges.Configured || merges.ScopeID != "scope.home" || len(merges.Merges) != 0 {
		t.Fatal("merge list result", merges, err)
	}
	if _, err := client.CallWithParams(context.Background(), api.MethodDeviceMerges, map[string]any{"scope_id": "scope.other"}); err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatal("merge list accepted caller scope", err)
	}
}

func TestDeviceIdentityRejectsUnknownAndMissingParameters(t *testing.T) {
	server := startMutationTestServer(t, &mutationTestHandler{})
	client := NewClient(server.stateDir)
	for method, cases := range map[string][]any{
		api.MethodDeviceMerge: {
			nil,
			map[string]any{"source_device_id": "device.source"},
			map[string]any{"source_device_id": "device.source", "target_device_id": "device.target", "extra": true},
		},
		api.MethodDeviceUnmerge: {
			nil,
			map[string]any{"target_device_id": "device.target"},
			map[string]any{"source_device_id": "device.source", "target_device_id": "device.target"},
		},
	} {
		for _, params := range cases {
			var err error
			if params == nil {
				_, err = client.Call(context.Background(), method)
			} else {
				_, err = client.CallWithParams(context.Background(), method, params)
			}
			if err == nil || !strings.Contains(err.Error(), "invalid_request") {
				t.Fatalf("%s %+v: %v", method, params, err)
			}
		}
	}
}

func TestDeviceIdentityMapsSafeHandlerErrors(t *testing.T) {
	for _, tc := range []struct {
		handlerErr error
		code       string
	}{
		{ErrInvalidMutation, "invalid_request"},
		{ErrMutationTargetNotFound, "not_found"},
		{ErrMutationConflict, "conflict"},
	} {
		server := startMutationTestServer(t, &mutationTestHandler{identityErr: tc.handlerErr})
		_, err := NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodDeviceMerge,
			api.DeviceMergeParams{SourceDeviceID: "device.source", TargetDeviceID: "device.target"})
		if err == nil || !strings.Contains(err.Error(), tc.code) {
			t.Fatal(tc.code, err)
		}
	}
}
