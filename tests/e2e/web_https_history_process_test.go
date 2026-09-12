package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestWebProcessHTTPSHistoryReadsRetainedAuditsWithoutExecution(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Unix process boundary evidence; synthetic retained audits, no active checks")
	}
	binary, uiDir := os.Getenv(e2eBinaryEnv), os.Getenv(e2eUIDirEnv)
	if binary == "" || uiDir == "" {
		t.Skip("set process E2E binary and built UI directory")
	}
	var err error
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	uiDir, err = filepath.Abs(uiDir)
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir includes the long test name and a variable-length suffix, which
	// can reach Linux's 108-byte sockaddr_un limit before the socket terminator.
	fixtureRoot := shortProcessTempDir(t)
	stateDir := filepath.Join(fixtureRoot, "state ?#%")
	// Seed only synthetic durable events before the real controller owns SQLite.
	// No actual route, neighbor-cache or HTTPS work is needed to read old records.
	s, err := storage.Open(stateDir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute).Round(0)
	scope := domain.NetworkScope{ID: "scope.fixture", Kind: "lan", EnrolledAt: now.Add(-time.Hour),
		Metadata: json.RawMessage(`{"device_watch":{"interface_name":"fixture0","interface_index":7,"prefixes":["192.168.50.0/24"]}}`)}
	if err := s.CreateNetworkScope(context.Background(), scope); err != nil {
		s.Close()
		t.Fatal(err)
	}
	event := httpsrun.Event{SchemaVersion: 1, RunID: strings.Repeat("a", 32), Profile: httpsrun.Profile, At: now,
		Selection: nq.HTTPSSelection{ID: "selection.test", EndpointID: "endpoint.test", RequestID: "request.test", Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 503},
		Observer:  nq.Observer{ScopeID: scope.ID, SensorID: "sensor.test", InterfaceName: "fixture0", InterfaceIndex: 7}}
	end, zero := now.Add(time.Second), int64(0)
	for _, phase := range []string{"authorized", "admitted", "finished"} {
		event.State = phase
		if phase == "finished" {
			event.At = end
			event.Outcome = "completed"
			event.Measurement = &httpsrun.Measurement{StartedAt: now, CompletedAt: end, Exchange: nq.HTTPSResponseReceived, Request: nq.HTTPSRequestAccepted, Stage: nq.HTTPSRequest, StatusCode: 503, ResponseTimeNanoseconds: &zero}
		}
		if err := s.InsertHTTPSRunAudit(context.Background(), event); err != nil {
			s.Close()
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	controller := startController(t, binary, stateDir)
	waitForReady(t, binary, stateDir, controller)
	assertHTTPSExecutionDisabled(t, stateDir)
	secret := readSecret(t, stateDir)
	web := startWeb(t, binary, stateDir, uiDir)
	root, origin, bootstrap := parseWebReadyURL(t, waitForWebReady(t, web))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar}
	response, err := client.Get(root + "api/network-quality/https-history")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("history did not require browser session")
	}
	request, err := http.NewRequest("POST", root+"api/session", strings.NewReader(`{"bootstrap":"`+bootstrap+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("bootstrap failed")
	}
	for n := 0; n < 2; n++ {
		response, err = client.Get(root + "api/network-quality/https-history")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || len(raw) > 65536 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("history unavailable: %s %v", raw, err)
		}
		for _, forbidden := range []string{secret, bootstrap, "scope_id", "selection_digest", "challenge", "ticket", "summary", "next_step", "profile", "reason", `"endpoint":`, "server_name", "request_target", "test.example", "192.168.50"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("history leaked %q", forbidden)
			}
		}
		var result struct {
			Enrolled bool `json:"enrolled"`
			Runs     []struct {
				RunID              string                  `json:"run_id"`
				Evidence           string                  `json:"evidence"`
				Outcome            string                  `json:"outcome"`
				Measurement        api.HTTPSRunMeasurement `json:"measurement"`
				ExpectationMatched *bool                   `json:"expectation_matched"`
			} `json:"runs"`
		}
		if json.Unmarshal(raw, &result) != nil || !result.Enrolled || len(result.Runs) != 1 || result.Runs[0].RunID != event.RunID || result.Runs[0].Evidence != "status-response" || result.Runs[0].Outcome != "completed" ||
			!result.Runs[0].Measurement.StartedAt.Equal(now) || result.Runs[0].Measurement.ResponseTimeNanoseconds == nil || *result.Runs[0].Measurement.ResponseTimeNanoseconds != 0 || result.Runs[0].ExpectationMatched == nil || !*result.Runs[0].ExpectationMatched {
			t.Fatalf("changed retained evidence: %s", raw)
		}
	}
	for _, suffix := range []string{"?run_id=" + event.RunID, "?target=192.168.50.1", "?approve=true"} {
		response, err = client.Get(root + "api/network-quality/https-history" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal("accepted browser selector or consent")
		}
	}
	coverage := runCLIJSONArgs[map[string]any](t, binary, "device-watch-coverage", "--state-dir", stateDir)
	if coverage["configured"] != false {
		t.Fatal("history enabled Device Watch")
	}
	stopWithInterrupt(t, web)
	stopWithInterrupt(t, controller)
	s, err = storage.Open(stateDir, storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.ReadHTTPSHistory(context.Background(), storage.HTTPSHistoryQuery{ScopeID: scope.ID, AsOf: time.Now().UTC()})
	if err != nil || len(page.Runs) != 1 || !page.Runs[0].TerminalRetained || page.Runs[0].Measurement.StatusCode != 503 {
		t.Fatal("history read changed durable sample", err)
	}
	if count, err := s.ObservationCount(context.Background()); err != nil || count != 0 {
		t.Fatal("history collected observations", err)
	}
}

func assertHTTPSExecutionDisabled(t *testing.T, stateDir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := localapi.NewClient(stateDir).CheckHTTPS(ctx, "https-selection."+strings.Repeat("a", 32), func(context.Context, api.HTTPSCheckReview) (bool, error) {
		t.Error("default controller offered a HTTPS review")
		return false, nil
	})
	var response *localapi.ResponseError
	if !errors.As(err, &response) || response.Code != "unavailable" || result.RunID != "" || result.Measurement != nil {
		t.Fatalf("default controller exposed https execution: %+v %v", result, err)
	}
}
