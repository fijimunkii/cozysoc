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
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
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
	assertNativeGatewayHistory(t, ctx, client, work, state, runID)
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
	t.Run("https-native-review", func(t *testing.T) {
		raw, err := client.CallWithParams(ctx, api.MethodHTTPSSave, api.HTTPSSettingsParams{Endpoint: target + ":443", ServerName: "test.example", RequestTarget: "/check", Family: "ipv4", Method: "HEAD", ExpectedStatus: 204, DestinationPolicy: "exact-endpoint"})
		if err != nil {
			t.Fatal(err)
		}
		var saved api.HTTPSSettingsResult
		if json.Unmarshal(raw, &saved) != nil || len(saved.Items) != 1 {
			t.Fatal("HTTPS save failed")
		}
		output, err := exec.CommandContext(ctx, filepath.Join(work, "cozysoc"), "https-plan", "--state-dir", state, saved.Items[0].SelectionID).Output()
		if err != nil {
			t.Fatal(err)
		}
		var review api.HTTPSPlan
		if json.Unmarshal(output, &review) != nil || review.Mode != "preview-only" || review.ExecutionAvailable || review.ConsentGranted || review.Configuration.SelectionID != saved.Items[0].SelectionID || review.RequestBytes == "" || review.Source == "" || !time.Now().Before(review.RouteFreshUntil) {
			t.Fatal("native HTTPS review failed")
		}
	})
	var dnsRunID string
	t.Run("resolver-native-session", func(t *testing.T) { dnsRunID = nativeResolverCLI(t, ctx, client, work, python) })
	stop() // Join the actual controller before reading its persisted evidence.
	if t.Failed() {
		return // Never read SQLite after an unconfirmed child shutdown.
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "cozysoc.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dnsRaw string
	if err := db.QueryRowContext(ctx, `SELECT payload FROM audit_events WHERE id=?`, "audit.resolver-run."+dnsRunID+".finished").Scan(&dnsRaw); err != nil {
		t.Fatal(err)
	}
	var dnsEvent resolverrun.Event
	if json.Unmarshal([]byte(dnsRaw), &dnsEvent) != nil || resolverrun.ValidateEvent(dnsEvent) != nil || dnsEvent.Outcome != "completed" || dnsEvent.Measurement == nil || dnsEvent.Measurement.Reply == nil || dnsEvent.Measurement.Reply.RCode != 0 {
		t.Fatal("native resolver audit missing")
	}
	var dnsCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE kind='resolver-run'`).Scan(&dnsCount); err != nil || dnsCount != 3 {
		t.Fatal("resolver declines admitted work or phases duplicated", err)
	}
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
func interactiveGatewayCLI(t *testing.T, ctx context.Context, work, python, mode string, selection ...string) string {
	t.Helper()
	args := append([]string{filepath.Join(work, "cli-driver.py"), mode}, selection...)
	command := exec.Command(python, args...)
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

// Inspect the same retained run through the read API and built command while
// the independent peer is still counting. These reads cannot send a fourth echo.
func assertNativeGatewayHistory(t *testing.T, ctx context.Context, client *localapi.Client, work, state, runID string) {
	t.Helper()
	var reference *api.GatewayRunMeasurement
	for _, id := range []string{"", runID, runID} {
		var raw json.RawMessage
		var err error
		if id == "" {
			raw, err = client.Call(ctx, api.MethodGatewayHistory)
		} else {
			raw, err = client.CallWithParams(ctx, api.MethodGatewayHistory, api.GatewayHistoryParams{RunID: id})
		}
		if err != nil {
			t.Fatal(err)
		}
		var history api.GatewayHistory
		if json.Unmarshal(raw, &history) != nil || history.Mode != "retained-history" || !history.Enrolled || len(history.Runs) != 1 || history.Truncated || history.ScanTruncated {
			t.Fatal("native history lost the retained run")
		}
		r := history.Runs[0]
		if r.RunID != runID || r.Target != target || r.Source != source || r.Outcome != "completed" || r.Measurement == nil || !r.Measurement.Complete || r.Measurement.Replies != 3 || r.Assessment.State != "all-replied" || r.GatewayRoleVerified || !r.TerminalRetained || !r.AdmissionRetained || !r.AuthorizationRetained {
			t.Fatal("native retained assessment changed measurement or provenance")
		}
		if reference != nil && (!r.Measurement.StartedAt.Equal(reference.StartedAt) || !r.Measurement.CompletedAt.Equal(*reference.CompletedAt)) {
			t.Fatal("history read refreshed measurement timestamps")
		}
		reference = r.Measurement
	}
	// Read-only history supports redirected stdout; unlike check, it grants no consent.
	command := exec.CommandContext(ctx, filepath.Join(work, "cozysoc"), "network-quality-history", "--state-dir", state, runID)
	out, err := command.Output()
	var history api.GatewayHistory
	if err != nil || json.Unmarshal(out, &history) != nil || len(history.Runs) != 1 || history.Runs[0].RunID != runID {
		t.Fatalf("built history command failed: %v", err)
	}
}

func nativeResolverCLI(t *testing.T, ctx context.Context, client *localapi.Client, work, python string) string {
	t.Helper()
	raw, err := client.CallWithParams(ctx, api.MethodResolverSave, api.ResolverSettingsParams{Endpoint: target + ":53", Name: "test.example.", Family: "ipv4", Transport: "udp", QueryType: "A", Expect: "answer", DestinationScope: "enrolled-prefix"})
	if err != nil {
		t.Fatal(err)
	}
	var settings api.ResolverSettingsResult
	if json.Unmarshal(raw, &settings) != nil || len(settings.Items) != 1 || settings.ConsentGranted {
		t.Fatal("resolver settings failed")
	}
	id := settings.Items[0].SelectionID
	peer := startPeer(t, "dns-answer")
	for _, mode := range []string{"redirected-input", "redirected-output", "decline", "preloaded", "overlong", "eof", "interrupt"} {
		interactiveGatewayCLI(t, ctx, work, python, "dns-"+mode, id)
	}
	output := interactiveGatewayCLI(t, ctx, work, python, "dns-approve", id)
	match := regexp.MustCompile(`(?m)^Run: ([0-9a-f]{32})$`).FindStringSubmatch(output)
	if len(match) != 2 {
		t.Fatal("resolver CLI lost run reference")
	}
	assertNativeResolverHistory(t, ctx, client, work, id, match[1])
	peer.stop(t)
	queries, summaries := 0, 0
	for _, e := range peer.events {
		switch e.Event {
		case "ready":
		case "query":
			queries++
			if e.Bytes != 30 {
				t.Fatal("DNS question size changed")
			}
		case "summary":
			summaries++
			if e.Queries != 1 || e.Echoes != 0 {
				t.Fatal("unexpected probe traffic")
			}
		default:
			t.Fatal("unexpected peer evidence")
		}
	}
	if queries != 1 || summaries != 1 {
		t.Fatal("missing DNS wire evidence")
	}
	if _, err := client.CheckResolver(ctx, id, func(context.Context, api.ResolverCheckReview) (bool, error) {
		t.Error("resolver cooldown issued review")
		return true, nil
	}); err == nil {
		t.Fatal("resolver cooldown disappeared")
	}
	return match[1]
}

// Read list/exact/CLI history after configuration retirement while the peer
// still counts packets. History cannot require active settings or revive consent.
func assertNativeResolverHistory(t *testing.T, ctx context.Context, client *localapi.Client, work, selectionID, runID string) {
	t.Helper()
	if _, err := client.CallWithParams(ctx, api.MethodResolverRetire, api.ResolverIDParams{SelectionID: selectionID}); err != nil {
		t.Fatal(err)
	}
	var original *api.ResolverRunMeasurement
	for _, id := range []string{"", runID, runID} {
		var raw json.RawMessage
		var err error
		if id == "" {
			raw, err = client.Call(ctx, api.MethodResolverHistory)
		} else {
			raw, err = client.CallWithParams(ctx, api.MethodResolverHistory, api.ResolverHistoryParams{RunID: id})
		}
		var history api.ResolverHistory
		if err != nil || json.Unmarshal(raw, &history) != nil || history.Mode != "retained-history" || !history.Enrolled || len(history.Runs) != 1 || history.Truncated || history.ScanTruncated {
			t.Fatal("resolver history unavailable", err)
		}
		r := history.Runs[0]
		if r.RunID != runID || r.Selection.ID != selectionID || r.Outcome != "completed" || r.Measurement == nil || r.Measurement.Reply == nil || r.Measurement.Reply.RCode != 0 || r.Assessment.State != "answer" || r.Assessment.ExpectationMatched == nil || !*r.Assessment.ExpectationMatched || !r.AuthorizationRetained || !r.AdmissionRetained || !r.TerminalRetained {
			t.Fatal("resolver history lost provenance/evidence")
		}
		if original != nil && (!original.StartedAt.Equal(r.Measurement.StartedAt) || !original.CompletedAt.Equal(r.Measurement.CompletedAt)) {
			t.Fatal("history refreshed original timestamps")
		}
		original = r.Measurement
	}
	command := exec.CommandContext(ctx, filepath.Join(work, "cozysoc"), "resolver-history", "--state-dir", filepath.Join(work, "controller-state"), runID)
	out, err := command.Output()
	var history api.ResolverHistory
	if err != nil || json.Unmarshal(out, &history) != nil || len(history.Runs) != 1 || history.Runs[0].RunID != runID {
		t.Fatal("resolver history CLI unavailable", err)
	}
}
