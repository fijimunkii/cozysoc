package macoslab

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	_ "modernc.org/sqlite"
)

// Run the actual serve command, not an injected handler. The private lab monitor
// owns its child and stops/joins it on stdin EOF even if this test process dies.
func nativeConsentSession(t *testing.T) {
	t.Helper()
	work := filepath.Dir(os.Getenv("COZYSOC_LAB_PEER"))
	python := os.Getenv("COZYSOC_LAB_PYTHON")
	if !filepath.IsAbs(work) || !filepath.IsAbs(python) {
		t.Fatal("missing native process fixture")
	}
	state := filepath.Join(work, "controller-state")
	command := exec.Command(python, filepath.Join(work, "run.py"), "controller")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(work, "controller-supervisor.log"))
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = input.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("controller supervisor did not confirm clean exit: %v", err)
				}
			case <-time.After(12 * time.Second):
				t.Error("controller supervisor did not drain")
			}
			_ = log.Close()
		})
	}
	t.Cleanup(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	client := localapi.NewClient(state)
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		probe, stopProbe := context.WithTimeout(ctx, 250*time.Millisecond)
		_, err = client.Call(probe, api.MethodStatus)
		stopProbe()
		if err == nil {
			ready = true
			break
		}
		select {
		case <-done:
			t.Fatal("controller exited before readiness")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("native controller unavailable")
	}
	confirm := func(context.Context, api.GatewayCheckReview) (bool, error) {
		t.Error("startup quiet interval granted a review")
		return false, nil
	}
	if _, err := client.CheckGateway(ctx, target, confirm); err == nil {
		t.Fatal("missing startup quiet interval")
	} else {
		var response *localapi.ResponseError
		if !errors.As(err, &response) || response.Code != "cooldown" {
			t.Fatalf("startup gate: %v", err)
		}
	}
	if _, err := client.CallWithParams(ctx, api.MethodNetworkEnroll, api.NetworkEnrollParams{InterfaceName: "feth42"}); err != nil {
		t.Fatal(err)
	}
	// This process uses the full REAL startup quiet interval, not a test clock,
	// flag, shortened budget or per-request replacement of its coordinator.
	timer := time.NewTimer(gatewayrun.RunInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	peer := startPeer(t, "reply")
	declined, err := client.CheckGateway(ctx, target, func(context.Context, api.GatewayCheckReview) (bool, error) { return false, nil })
	if err != nil || declined.Outcome != "declined" || declined.Measurement != nil {
		t.Fatalf("decline: %+v %v", declined, err)
	}
	// Drive the actual command with real pseudo-terminals, not an injected
	// confirmation callback. All negative cases precede the only approved run.
	for _, mode := range []string{"redirected-input", "redirected-output", "decline", "preloaded", "overlong", "eof", "interrupt", "expire"} {
		interactiveGatewayCLI(t, ctx, work, python, mode)
	}
	output := interactiveGatewayCLI(t, ctx, work, python, "approve")
	match := regexp.MustCompile(`(?m)^Run reference: ([0-9a-f]{32})$`).FindStringSubmatch(output)
	if len(match) != 2 {
		t.Fatal("interactive result lost its audit reference")
	}
	runID := match[1]
	peer.assertEchoes(t, 3)
	if _, err := client.CheckGateway(ctx, target, confirm); err == nil {
		t.Fatal("run cooldown disappeared")
	}
	raw, err := client.Call(ctx, api.MethodCapabilitiesList)
	if err != nil {
		t.Fatal(err)
	}
	var catalog api.CapabilityList
	if json.Unmarshal(raw, &catalog) != nil {
		t.Fatal("invalid capabilities")
	}
	for _, entry := range catalog.Capabilities {
		if entry.Manifest.ID == "device-watch" && entry.State.Desired != capability.DesiredDisabled {
			t.Fatal("check enabled Device Watch")
		}
	}
	stop() // Join the actual controller before reading its persisted evidence.
	if t.Failed() {
		return // Never read SQLite after an unconfirmed child shutdown.
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "cozysoc.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rawAudit string
	if err := db.QueryRowContext(ctx, `SELECT payload FROM audit_events WHERE id=?`, "audit.gateway-run."+runID+".finished").Scan(&rawAudit); err != nil {
		t.Fatal(err)
	}
	var event gatewayrun.Event
	if json.Unmarshal([]byte(rawAudit), &event) != nil || gatewayrun.ValidateEvent(event) != nil || event.Measurement == nil || event.Measurement.Replies != 3 || event.Outcome != "completed" || event.Target != target || event.Source != source {
		t.Fatal("native result lost its durable terminal evidence")
	}
	for _, query := range []string{`SELECT count(*) FROM observations`, `SELECT count(*) FROM coverage_samples`} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatal("gateway check created monitoring evidence", err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE kind='gateway-run'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("decline created consent or run phases duplicated", err)
	}
}

// The helper owns/reaps one CLI child. Its stdin pipe is lifetime authority only:
// parent crash/EOF aborts the helper's child; it never supplies approval input.
func interactiveGatewayCLI(t *testing.T, ctx context.Context, work, python, mode string) string {
	t.Helper()
	command := exec.Command(python, filepath.Join(work, "cli-driver.py"), mode)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	stop := context.AfterFunc(ctx, func() { _ = input.Close() })
	defer stop()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("interactive CLI %s: %v\n%s", mode, err, output)
	}
	var result struct {
		Mode     string `json:"mode"`
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if json.Unmarshal(output, &result) != nil || result.Mode != mode {
		t.Fatal("invalid interactive fixture result")
	}
	t.Logf("interactive CLI %s passed", mode)
	return result.Output
}
