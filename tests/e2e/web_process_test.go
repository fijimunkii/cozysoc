package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const e2eUIDirEnv = "COZYSOC_E2E_UI_DIR"

type processCoverageEnvelope struct {
	AsOf    time.Time               `json:"as_of"`
	Reports []processCoverageReport `json:"reports"`
}

type processWebActivity struct {
	Configured bool              `json:"configured"`
	ScopeID    string            `json:"scope_id,omitempty"`
	Since      time.Time         `json:"since"`
	AsOf       time.Time         `json:"as_of"`
	Items      []json.RawMessage `json:"items"`
	Truncated  bool              `json:"truncated"`
}

type processWebDeviceList struct {
	Configured bool              `json:"configured"`
	ScopeID    string            `json:"scope_id,omitempty"`
	AsOf       time.Time         `json:"as_of"`
	Devices    []json.RawMessage `json:"devices"`
	Truncated  bool              `json:"truncated"`
}

func TestWebProcessReadsCoverageWithoutOwningController(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("web process E2E currently targets the Linux CI reference runner")
	}
	binary := os.Getenv(e2eBinaryEnv)
	if binary == "" {
		t.Skipf("set %s to a built cozysoc binary to run process E2E", e2eBinaryEnv)
	}
	uiDir := os.Getenv(e2eUIDirEnv)
	if uiDir == "" {
		t.Skipf("set %s to a built UI directory to run web process E2E", e2eUIDirEnv)
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	absoluteUI, err := filepath.Abs(uiDir)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	controller := startController(t, absoluteBinary, stateDir)
	waitForReady(t, absoluteBinary, stateDir, controller)
	controllerSecret := readSecret(t, stateDir)

	cliCoverage := runCLIJSON[processCoverageEnvelope](t, absoluteBinary, stateDir, "coverage")
	if cliCoverage.AsOf.IsZero() || len(cliCoverage.Reports) != 1 || cliCoverage.Reports[0].CapabilityID != "device-watch" {
		t.Fatalf("unexpected generic CLI coverage: %+v", cliCoverage)
	}

	web := startWeb(t, absoluteBinary, stateDir, absoluteUI)
	readyURL := waitForWebReady(t, web)
	rootURL, origin, bootstrap := parseWebReadyURL(t, readyURL)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar}

	unauthenticated, err := client.Get(rootURL + "api/coverage")
	if err != nil {
		t.Fatalf("read unauthenticated web coverage: %v\n%s", err, web.logs())
	}
	_, _ = io.Copy(io.Discard, unauthenticated.Body)
	_ = unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated coverage status = %d, want 401", unauthenticated.StatusCode)
	}

	sessionRequest, err := http.NewRequest(http.MethodPost, rootURL+"api/session", strings.NewReader(`{"bootstrap":"`+bootstrap+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	sessionRequest.Header.Set("Content-Type", "application/json")
	sessionRequest.Header.Set("Origin", origin)
	sessionResponse, err := client.Do(sessionRequest)
	if err != nil {
		t.Fatalf("establish web session: %v\n%s", err, web.logs())
	}
	_, _ = io.Copy(io.Discard, sessionResponse.Body)
	_ = sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("web session status = %d, want 204", sessionResponse.StatusCode)
	}
	parsedRoot, err := url.Parse(rootURL)
	if err != nil {
		t.Fatal(err)
	}
	cookies := jar.Cookies(parsedRoot)
	if len(cookies) != 1 || cookies[0].Name != "cozysoc_session" || cookies[0].Value == "" || cookies[0].Value == bootstrap {
		t.Fatalf("unexpected browser session cookies: %+v", cookies)
	}
	webSession := cookies[0].Value

	reusedRequest, err := http.NewRequest(http.MethodPost, rootURL+"api/session", strings.NewReader(`{"bootstrap":"`+bootstrap+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	reusedRequest.Header.Set("Content-Type", "application/json")
	reusedRequest.Header.Set("Origin", origin)
	reusedResponse, err := client.Do(reusedRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, reusedResponse.Body)
	_ = reusedResponse.Body.Close()
	if reusedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused bootstrap status = %d, want 401", reusedResponse.StatusCode)
	}

	response, err := client.Get(rootURL + "api/coverage")
	if err != nil {
		t.Fatalf("read live web coverage: %v\n%s", err, web.logs())
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("coverage HTTP status = %d body=%s", response.StatusCode, body)
	}
	for _, secret := range []string{controllerSecret, bootstrap, webSession} {
		if strings.Contains(string(body), secret) {
			t.Fatal("web coverage response exposed a controller or browser credential")
		}
	}
	for _, forbidden := range []string{"operational", "neighbors_in_scope", "blind_spots", "filesystem_available_bytes"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("web coverage response exposed Device Watch-only detail %q: %s", forbidden, body)
		}
	}
	var coverage processCoverageEnvelope
	if err := json.Unmarshal(body, &coverage); err != nil {
		t.Fatalf("decode web coverage: %v: %s", err, body)
	}
	if coverage.AsOf.IsZero() || len(coverage.Reports) != 1 {
		t.Fatalf("unexpected web coverage envelope: %+v", coverage)
	}
	report := coverage.Reports[0]
	if report.CapabilityID != "device-watch" || report.Configured || report.State != "unconfigured" || len(report.ObservationPoints) != 0 {
		t.Fatalf("unexpected shared web coverage report: %+v", report)
	}

	activityResponse, err := client.Get(rootURL + "api/activity")
	if err != nil {
		t.Fatalf("read live web activity: %v\n%s", err, web.logs())
	}
	activityBody, err := io.ReadAll(activityResponse.Body)
	_ = activityResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if activityResponse.StatusCode != http.StatusOK {
		t.Fatalf("activity HTTP status = %d body=%s", activityResponse.StatusCode, activityBody)
	}
	for _, secret := range []string{controllerSecret, bootstrap, webSession} {
		if strings.Contains(string(activityBody), secret) {
			t.Fatal("web activity response exposed a controller or browser credential")
		}
	}
	var activity processWebActivity
	if err := json.Unmarshal(activityBody, &activity); err != nil {
		t.Fatalf("decode web activity: %v: %s", err, activityBody)
	}
	if activity.AsOf.IsZero() || activity.Since.IsZero() || activity.Configured || activity.ScopeID != "" || len(activity.Items) != 0 || activity.Truncated {
		t.Fatalf("unexpected fresh-state web activity: %+v", activity)
	}

	devicesResponse, err := client.Get(rootURL + "api/devices")
	if err != nil {
		t.Fatalf("read live web devices: %v\n%s", err, web.logs())
	}
	devicesBody, err := io.ReadAll(devicesResponse.Body)
	_ = devicesResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if devicesResponse.StatusCode != http.StatusOK {
		t.Fatalf("devices HTTP status = %d body=%s", devicesResponse.StatusCode, devicesBody)
	}
	for _, secret := range []string{controllerSecret, bootstrap, webSession} {
		if strings.Contains(string(devicesBody), secret) {
			t.Fatal("web devices response exposed a controller or browser credential")
		}
	}
	var devices processWebDeviceList
	if err := json.Unmarshal(devicesBody, &devices); err != nil {
		t.Fatalf("decode web devices: %v: %s", err, devicesBody)
	}
	if devices.AsOf.IsZero() || devices.Configured || devices.ScopeID != "" || len(devices.Devices) != 0 || devices.Truncated {
		t.Fatalf("unexpected fresh-state web devices: %+v", devices)
	}

	detailResponse, err := client.Get(rootURL + "api/devices/detail?device_id=device.missing")
	if err != nil {
		t.Fatalf("read missing device detail: %v", err)
	}
	detailBody, err := io.ReadAll(detailResponse.Body)
	_ = detailResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if detailResponse.StatusCode != http.StatusNotFound || !strings.Contains(string(detailBody), `"error":"not_found"`) {
		t.Fatalf("missing detail status=%d body=%s", detailResponse.StatusCode, detailBody)
	}
	for _, secret := range []string{controllerSecret, bootstrap, webSession} {
		if strings.Contains(string(detailBody), secret) {
			t.Fatal("device detail error exposed a controller or browser credential")
		}
	}

	root, err := client.Get(rootURL)
	if err != nil {
		t.Fatalf("read web root: %v", err)
	}
	rootBody, err := io.ReadAll(root.Body)
	_ = root.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if root.StatusCode != http.StatusOK || !strings.Contains(string(rootBody), `id="root"`) {
		t.Fatalf("unexpected web root: status=%d body=%s", root.StatusCode, rootBody)
	}

	wrongHostRequest, err := http.NewRequest(http.MethodGet, rootURL+"api/coverage", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongHostRequest.Host = "attacker.invalid"
	wrongHost, err := client.Do(wrongHostRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, wrongHost.Body)
	_ = wrongHost.Body.Close()
	if wrongHost.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong Host status = %d, want 403", wrongHost.StatusCode)
	}

	stopWithInterrupt(t, web)
	status := runCLIJSON[controllerStatus](t, absoluteBinary, stateDir, "status")
	if status.PID != controller.cmd.Process.Pid || status.Transport != "unix" {
		t.Fatalf("stopping web affected controller: %+v", status)
	}
	stopWithInterrupt(t, controller)
}

func startWeb(t *testing.T, binary, stateDir, uiDir string) *runningController {
	t.Helper()
	logDir := t.TempDir()
	stdoutPath := filepath.Join(logDir, "web-stdout.log")
	stderrPath := filepath.Join(logDir, "web-stderr.log")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		_ = stdout.Close()
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "web", "--state-dir", stateDir, "--listen", "127.0.0.1:0", "--ui-dir", uiDir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		wait <- err
		close(wait)
	}()
	web := &runningController{cmd: cmd, wait: wait, stdoutPath: stdoutPath, stderrPath: stderrPath}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return web
}

func waitForWebReady(t *testing.T, web *runningController) string {
	t.Helper()
	const prefix = "cozysoc web ready: "
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-web.wait:
			t.Fatalf("web process exited before readiness: %v\n%s", err, web.logs())
		default:
		}
		stdout, _ := os.ReadFile(web.stdoutPath)
		for _, line := range strings.Split(string(stdout), "\n") {
			if strings.HasPrefix(line, prefix) {
				readyURL := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if !strings.HasPrefix(readyURL, "http://127.0.0.1:") || !strings.Contains(readyURL, "/#bootstrap=") {
					t.Fatalf("unexpected web readiness URL %q", readyURL)
				}
				return readyURL
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("web process did not become ready\n%s", web.logs())
	return ""
}

func parseWebReadyURL(t *testing.T, readyURL string) (rootURL, origin, bootstrap string) {
	t.Helper()
	parsed, err := url.Parse(readyURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" {
		t.Fatalf("unexpected web URL authority: %s", readyURL)
	}
	params, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap = params.Get("bootstrap")
	if len(bootstrap) != 43 {
		t.Fatalf("unexpected bootstrap token length %d", len(bootstrap))
	}
	parsed.Fragment = ""
	rootURL = parsed.String()
	if !strings.HasSuffix(rootURL, "/") {
		rootURL += "/"
	}
	origin = parsed.Scheme + "://" + parsed.Host
	return rootURL, origin, bootstrap
}
