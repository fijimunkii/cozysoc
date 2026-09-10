package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type processWebSessionInfo struct {
	CSRFToken string `json:"csrf_token"`
}

type processWebNetworkList struct {
	Candidates []json.RawMessage `json:"candidates"`
	Enrolled   json.RawMessage   `json:"enrolled,omitempty"`
}

func TestWebProcessMutationBoundaryRejectsCSRFAndAvoidsRealNetworkEnrollment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("web mutation process E2E currently targets the Linux CI reference runner")
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
	web := startWeb(t, absoluteBinary, stateDir, absoluteUI)
	readyURL := waitForWebReady(t, web)
	rootURL, origin, bootstrap := parseWebReadyURL(t, readyURL)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar}

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

	sessionInfoResponse, err := client.Get(rootURL + "api/session")
	if err != nil {
		t.Fatal(err)
	}
	sessionInfoBody, err := io.ReadAll(sessionInfoResponse.Body)
	_ = sessionInfoResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if sessionInfoResponse.StatusCode != http.StatusOK {
		t.Fatalf("session info status=%d body=%s", sessionInfoResponse.StatusCode, sessionInfoBody)
	}
	var sessionInfo processWebSessionInfo
	if err := json.Unmarshal(sessionInfoBody, &sessionInfo); err != nil {
		t.Fatal(err)
	}
	if len(sessionInfo.CSRFToken) != 43 || sessionInfo.CSRFToken == bootstrap || strings.Contains(string(sessionInfoBody), controllerSecret) {
		t.Fatalf("unexpected CSRF session info: %s", sessionInfoBody)
	}

	networksResponse, err := client.Get(rootURL + "api/networks")
	if err != nil {
		t.Fatal(err)
	}
	networksBody, err := io.ReadAll(networksResponse.Body)
	_ = networksResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if networksResponse.StatusCode != http.StatusOK {
		t.Fatalf("networks status=%d body=%s", networksResponse.StatusCode, networksBody)
	}
	for _, secret := range []string{controllerSecret, bootstrap, sessionInfo.CSRFToken} {
		if strings.Contains(string(networksBody), secret) {
			t.Fatal("network read exposed a controller/browser credential")
		}
	}
	var before processWebNetworkList
	if err := json.Unmarshal(networksBody, &before); err != nil {
		t.Fatal(err)
	}
	if len(before.Enrolled) != 0 && string(before.Enrolled) != "null" {
		t.Fatalf("fresh test controller unexpectedly has an enrolled network: %s", before.Enrolled)
	}

	missingCSRF, err := http.NewRequest(http.MethodPost, rootURL+"api/device-watch/enable", nil)
	if err != nil {
		t.Fatal(err)
	}
	missingCSRF.Header.Set("Origin", origin)
	missingCSRFResponse, err := client.Do(missingCSRF)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, missingCSRFResponse.Body)
	_ = missingCSRFResponse.Body.Close()
	if missingCSRFResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("missing-CSRF enable status=%d, want 403", missingCSRFResponse.StatusCode)
	}

	validEnable, err := http.NewRequest(http.MethodPost, rootURL+"api/device-watch/enable", nil)
	if err != nil {
		t.Fatal(err)
	}
	validEnable.Header.Set("Origin", origin)
	validEnable.Header.Set("X-Cozy-CSRF", sessionInfo.CSRFToken)
	validEnableResponse, err := client.Do(validEnable)
	if err != nil {
		t.Fatal(err)
	}
	validEnableBody, err := io.ReadAll(validEnableResponse.Body)
	_ = validEnableResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if validEnableResponse.StatusCode != http.StatusPreconditionFailed || !strings.Contains(string(validEnableBody), `"error":"precondition_failed"`) {
		t.Fatalf("fresh-state enable status=%d body=%s", validEnableResponse.StatusCode, validEnableBody)
	}

	enrollRequest, err := http.NewRequest(http.MethodPost, rootURL+"api/networks/enroll", strings.NewReader(`{"interface_name":"cozysoc-no-such-iface"}`))
	if err != nil {
		t.Fatal(err)
	}
	enrollRequest.Header.Set("Origin", origin)
	enrollRequest.Header.Set("X-Cozy-CSRF", sessionInfo.CSRFToken)
	enrollRequest.Header.Set("Content-Type", "application/json")
	enrollResponse, err := client.Do(enrollRequest)
	if err != nil {
		t.Fatal(err)
	}
	enrollBody, err := io.ReadAll(enrollResponse.Body)
	_ = enrollResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if enrollResponse.StatusCode != http.StatusPreconditionFailed || !strings.Contains(string(enrollBody), `"error":"precondition_failed"`) {
		t.Fatalf("invalid-interface enrollment status=%d body=%s", enrollResponse.StatusCode, enrollBody)
	}

	afterResponse, err := client.Get(rootURL + "api/networks")
	if err != nil {
		t.Fatal(err)
	}
	afterBody, err := io.ReadAll(afterResponse.Body)
	_ = afterResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var after processWebNetworkList
	if err := json.Unmarshal(afterBody, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Enrolled) != 0 && string(after.Enrolled) != "null" {
		t.Fatalf("process E2E must not enroll a runner network: %s", after.Enrolled)
	}

	stopWithInterrupt(t, web)
	status := runCLIJSON[controllerStatus](t, absoluteBinary, stateDir, "status")
	if status.PID != controller.cmd.Process.Pid {
		t.Fatalf("stopping web affected controller: %+v", status)
	}
	stopWithInterrupt(t, controller)
}
