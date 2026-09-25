package macoslab

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

func assertNoDeviceWatchEvidence(t *testing.T, ctx context.Context, state string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "cozysoc.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"observations", "evidence_batch_lookup", "coverage_samples"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("approved gateway check created monitoring evidence", table, count, err)
		}
	}
}

func nativeDeviceWatchDiscovery(t *testing.T, ctx context.Context, client *localapi.Client) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	neighbor, err := exec.CommandContext(readCtx, "/usr/sbin/arp", "-an", "-i", "feth42").Output()
	if err != nil || !strings.Contains(string(neighbor), "("+target+")") {
		t.Fatal("isolated peer was not present in the host neighbor cache", err)
	}

	raw, err := client.Call(ctx, api.MethodDeviceWatchEnable)
	if err != nil {
		t.Fatal(err)
	}
	var enabled api.DeviceWatchControlResult
	if err := json.Unmarshal(raw, &enabled); err != nil || !enabled.Active || enabled.ScopeID == "" {
		t.Fatal("native Device Watch did not start", err)
	}
	disabled := false
	t.Cleanup(func() {
		if disabled {
			return
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if _, err := client.Call(stopCtx, api.MethodDeviceWatchDisable); err != nil {
			t.Error("could not stop native Device Watch", err)
		}
	})

	var observed api.DeviceActivityItem
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err = client.Call(ctx, api.MethodDeviceActivity)
		if err != nil {
			t.Fatal(err)
		}
		var activity api.DeviceActivityList
		if err := json.Unmarshal(raw, &activity); err != nil {
			t.Fatal(err)
		}
		for _, item := range activity.Items {
			if item.Address == target && item.Kind == "first-observed" && item.Source.Attribution == "device-watch:arp-cache" {
				observed = item
				break
			}
		}
		if observed.DeviceID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Logf("last activity: %s", raw)
			if snapshot, readErr := client.Call(ctx, api.MethodDeviceWatchCoverage); readErr == nil {
				var report api.DeviceWatchCoverage
				if json.Unmarshal(snapshot, &report) == nil {
					neighbors := -1
					if report.NeighborsInScope != nil {
						neighbors = *report.NeighborsInScope
					}
					t.Logf("coverage: state=%s reason=%s neighbors=%d operational=%+v", report.State, report.Reason, neighbors, report.Operational)
				}
			} else {
				t.Logf("coverage read failed: %v", readErr)
			}
			if snapshot, readErr := client.Call(ctx, api.MethodDevicesList); readErr == nil {
				t.Logf("device list: %s", snapshot)
			} else {
				t.Logf("device list read failed: %v", readErr)
			}
			t.Logf("isolated neighbor cache: %s", neighbor)
			t.Fatal("native passive collection did not publish the isolated peer")
		}
		time.Sleep(100 * time.Millisecond)
	}

	raw, err = client.Call(ctx, api.MethodDevicesList)
	if err != nil {
		t.Fatal(err)
	}
	var devices api.DeviceList
	if err := json.Unmarshal(raw, &devices); err != nil || !devices.Configured || devices.ScopeID != enabled.ScopeID {
		t.Fatal("native device list lost its enrolled scope", err)
	}
	found := false
	for _, device := range devices.Devices {
		if device.ID == observed.DeviceID && device.State == "visible" && !device.FirstSeen.IsZero() && !device.LastSeen.IsZero() {
			found = true
		}
	}
	if !found {
		t.Fatal("native peer was not visible in the device list")
	}
	raw, err = client.CallWithParams(ctx, api.MethodDeviceDetail, api.DeviceDetailParams{DeviceID: observed.DeviceID})
	if err != nil {
		t.Fatal(err)
	}
	var detail api.DeviceDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, evidence := range detail.Evidence {
		if evidence.Kind == "ipv4" && evidence.Value == target && evidence.Source != nil && evidence.Source.Attribution == "device-watch:arp-cache" {
			found = true
		}
	}
	if !found {
		t.Fatal("native peer detail lost its original ARP provenance")
	}

	label := "Isolated lab peer"
	raw, err = client.CallWithParams(ctx, api.MethodDeviceLabel, api.DeviceLabelParams{DeviceID: observed.DeviceID, Label: &label})
	if err != nil {
		t.Fatal(err)
	}
	var labeled api.DeviceLabelResult
	if err := json.Unmarshal(raw, &labeled); err != nil || !labeled.Changed || labeled.UserLabel != label {
		t.Fatal("native peer label was not saved", err)
	}
	raw, err = client.Call(ctx, api.MethodDevicesList)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &devices); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, device := range devices.Devices {
		if device.ID == observed.DeviceID && device.UserLabel == label {
			found = true
		}
	}
	if !found {
		t.Fatal("native device list lost the saved label")
	}

	var coverage api.DeviceWatchCoverage
	for {
		raw, err = client.Call(ctx, api.MethodDeviceWatchCoverage)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &coverage); err != nil {
			t.Fatal(err)
		}
		if coverage.NeighborsInScope != nil && *coverage.NeighborsInScope >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native passive collection did not publish coverage")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if coverage.Coverage == nil || len(coverage.Coverage.ObservationPoints) != 1 || len(coverage.Coverage.ObservationPoints[0].Directions) != 0 {
		t.Fatal("native discovery overstated its passive coverage")
	}
	raw, err = client.Call(ctx, api.MethodDeviceWatchDisable)
	if err != nil {
		t.Fatal(err)
	}
	var stopped api.DeviceWatchControlResult
	if err := json.Unmarshal(raw, &stopped); err != nil || stopped.Active {
		t.Fatal("native Device Watch did not stop", err)
	}
	disabled = true
}
