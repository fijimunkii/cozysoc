package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

func TestWebResolverSettingsMutations(t *testing.T) {
	fixture := resolverWebFixture(t).Configuration
	h := newMutationTestHandler(t)
	saves, retires := 0, 0
	h.saveResolverSettings = func(_ context.Context, p api.ResolverSettingsParams) (api.ResolverSettingsResult, error) {
		saves++
		if p != fixture.Settings {
			t.Fatalf("unexpected save: %+v", p)
		}
		return api.ResolverSettingsResult{SchemaVersion: 1, Mode: "configuration-only", Items: []api.ResolverSettings{fixture}}, nil
	}
	h.retireResolverSettings = func(_ context.Context, p api.ResolverIDParams) (api.ResolverRetireResult, error) {
		retires++
		if p.SelectionID != fixture.SelectionID {
			t.Fatal(p.SelectionID)
		}
		return api.ResolverRetireResult{SchemaVersion: 1, SelectionID: p.SelectionID, State: "retired"}, nil
	}
	saveBody := `{"endpoint":"192.168.50.53:53","name":"example.invalid.","family":"ipv4","transport":"udp","query_type":"A","expect":"answer","destination_scope":"enrolled-prefix"}`
	save := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, gatewayWebRequest("/api/network-quality/resolver/selections/save", body))
		return w
	}
	w := save(saveBody)
	if w.Code != 200 || !strings.Contains(w.Body.String(), fixture.SelectionID) || strings.Contains(w.Body.String(), "scope.home") || saves != 1 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{
		`{"endpoint":"192.168.50.53:53"}`,
		strings.Replace(saveBody, `"endpoint":"192.168.50.53:53",`, `"endpoint":"192.168.50.53:53","endpoint":"192.168.50.53:53",`, 1),
		strings.Replace(saveBody, `"endpoint":`, `"approve":true,"endpoint":`, 1),
		strings.Replace(saveBody, `"name":"example.invalid."`, `"name":"example.invalid"`, 1),
		saveBody + `{}`,
	} {
		w = save(body)
		if w.Code != 400 || saves != 1 {
			t.Fatalf("accepted invalid save %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	retirePath := "/api/network-quality/resolver/selections/retire"
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
	r := gatewayWebRequest("/api/network-quality/resolver/selections/save", saveBody)
	r.Header.Del(webCSRFHeader)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden && w.Code != http.StatusUnauthorized || saves != 1 {
		t.Fatalf("accepted missing CSRF: %d %s", w.Code, w.Body.String())
	}
	h.saveResolverSettings = func(context.Context, api.ResolverSettingsParams) (api.ResolverSettingsResult, error) {
		return api.ResolverSettingsResult{}, errors.New("connection lost")
	}
	w = save(saveBody)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "mutation_outcome_unknown") || !strings.Contains(w.Body.String(), "reload") {
		t.Fatalf("uncertain save: %d %s", w.Code, w.Body.String())
	}
}
