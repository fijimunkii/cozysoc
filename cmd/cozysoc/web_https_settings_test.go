package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestWebHTTPSSettingsMutations(t *testing.T) {
	fixture := httpsWebFixture(t).Configuration
	h := newMutationTestHandler(t)
	saves, retires := 0, 0
	h.saveHTTPSSettings = func(_ context.Context, p api.HTTPSSettingsParams) (api.HTTPSSettingsResult, error) {
		saves++
		if p != fixture.Settings {
			t.Fatalf("unexpected save: %+v", p)
		}
		return api.HTTPSSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.HTTPSSettings{fixture}}, nil
	}
	h.retireHTTPSSettings = func(_ context.Context, p api.HTTPSIDParams) (api.HTTPSRetireResult, error) {
		retires++
		if p.SelectionID != fixture.SelectionID {
			t.Fatal(p.SelectionID)
		}
		return api.HTTPSRetireResult{SchemaVersion: 1, SelectionID: p.SelectionID, State: "retired"}, nil
	}
	saveBody := `{"endpoint":"192.168.50.53:443","server_name":"private.example","request_target":"/check?test=1","family":"ipv4","method":"HEAD","expected_status":204,"destination_policy":"exact-endpoint"}`
	save := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/https/selections/save", body))
		return w
	}
	w := save(saveBody)
	if w.Code != 200 || !strings.Contains(w.Body.String(), fixture.SelectionID) || strings.Contains(w.Body.String(), "scope.home") || saves != 1 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{
		`{"endpoint":"192.168.50.53:443"}`,
		strings.Replace(saveBody, `"endpoint":"192.168.50.53:443",`, `"endpoint":"192.168.50.53:443","endpoint":"192.168.50.53:443",`, 1),
		strings.Replace(saveBody, `"endpoint":`, `"approve":true,"endpoint":`, 1),
		strings.Replace(saveBody, `"request_target":"/check?test=1"`, `"request_target":"https://private.example/"`, 1),
		saveBody + `{}`,
	} {
		w = save(body)
		if w.Code != 400 || saves != 1 {
			t.Fatalf("accepted invalid save %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	retirePath := "/api/network-quality/https/selections/retire"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, gatewayWebRequest(retirePath, `{"selection_id":"`+fixture.SelectionID+`"}`))
	if w.Code != 200 || retires != 1 || !strings.Contains(w.Body.String(), `"state":"retired"`) {
		t.Fatalf("retire: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"selection_id":"bad"}`, `{"selection_id":"` + fixture.SelectionID + `","approve":true}`} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, gatewayWebRequest(retirePath, body))
		if w.Code != 400 || retires != 1 {
			t.Fatalf("accepted invalid retirement: %d %s", w.Code, w.Body.String())
		}
	}
	h.saveHTTPSSettings = func(context.Context, api.HTTPSSettingsParams) (api.HTTPSSettingsResult, error) {
		return api.HTTPSSettingsResult{}, errors.New("connection lost")
	}
	w = save(saveBody)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "mutation_outcome_unknown") || !strings.Contains(w.Body.String(), "reload") {
		t.Fatalf("uncertain save: %d %s", w.Code, w.Body.String())
	}
}
