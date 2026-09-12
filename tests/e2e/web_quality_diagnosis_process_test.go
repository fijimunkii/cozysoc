package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func assertWebQualityDiagnosis(t *testing.T, binary, dir, uiDir string, native api.QualityDiagnosis) {
	t.Helper()
	web := startWeb(t, binary, dir, uiDir)
	root, origin, bootstrap := parseWebReadyURL(t, waitForWebReady(t, web))
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Jar: jar}
	response, err := client.Get(root + "api/network-quality/diagnosis")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("diagnosis did not require session")
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
	for i := 0; i < 2; i++ {
		response, err = client.Get(root + "api/network-quality/diagnosis")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || len(raw) > 65536 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("diagnosis read failed: %s %v", raw, err)
		}
		for _, forbidden := range []string{readSecret(t, dir), bootstrap, `"scope_id"`, `"summary"`, `"next_step"`, `"limitations"`, `"challenge"`, `"ticket"`, `"endpoint"`, `"name"`} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatal("diagnosis leaked protected metadata")
			}
		}
		var out api.QualityDiagnosis
		if json.Unmarshal(raw, &out) != nil || out.Conclusion != native.Conclusion || out.Confidence != native.Confidence || !reflect.DeepEqual(out.Selected, native.Selected) || !reflect.DeepEqual(out.Compared, native.Compared) || out.AssessmentAt == nil || !out.AssessmentAt.Equal(*native.AssessmentAt) || out.EvidenceStart == nil || !out.EvidenceStart.Equal(*native.EvidenceStart) {
			t.Fatalf("changed historical comparison: %s", raw)
		}
	}
	for _, suffix := range []string{"?", "?approve=true", "?scope_id=scope.other", "?as_of=2026-09-12T12:00:00Z"} {
		response, err = client.Get(root + "api/network-quality/diagnosis" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatal("browser supplied diagnosis policy or authority")
		}
	}
	stopWithInterrupt(t, web)
}
