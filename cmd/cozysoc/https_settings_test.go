package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func httpsParams() api.HTTPSSettingsParams {
	return api.HTTPSSettingsParams{Endpoint: "198.51.100.20:443", ServerName: "Private.Example", RequestTarget: "/check?test=1", Family: "ipv4", Method: "HEAD", ExpectedStatus: 204, DestinationPolicy: "exact-endpoint"}
}
func TestHTTPSSettingsUseEnrollmentWithoutCollection(t *testing.T) {
	h, s, calls := resolverHandler(t)
	ctx := context.Background()
	result, err := h.SaveHTTPS(ctx, httpsParams())
	if err != nil || len(result.Items) != 1 || result.ConsentGranted || result.Mode != "configuration-only" {
		t.Fatalf("save: %+v %v", result, err)
	}
	saved := result.Items[0]
	if !localapi.ValidHTTPSSelectionID(saved.SelectionID) || saved.Settings.ServerName != "private.example" || saved.Profile != "selected-https-v1" || saved.Settings.Endpoint != httpsParams().Endpoint {
		t.Fatal("incorrect saved disclosure")
	}
	listed, err := h.ListHTTPSSettings(ctx)
	if err != nil || len(listed.Items) != 1 || listed.Items[0] != saved || *calls != 0 {
		t.Fatal("list changed settings or collected route")
	}
	if _, err = h.RetireHTTPS(ctx, api.HTTPSIDParams{SelectionID: saved.SelectionID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActiveHTTPSConfiguration(ctx, saved.SelectionID); err != storage.ErrHTTPSConfiguration {
		t.Fatal("retired setting still active")
	}
	listed, err = h.ListHTTPSSettings(ctx)
	if err != nil || len(listed.Items) != 0 || *calls != 0 {
		t.Fatal("retirement invented observations")
	}
}
func TestHTTPSConfigurationRejectsDefaultsBeforeWriting(t *testing.T) {
	h, _, _ := resolverHandler(t)
	for _, edit := range []func(*api.HTTPSSettingsParams){
		func(p *api.HTTPSSettingsParams) { p.Endpoint = "https://private.example" }, func(p *api.HTTPSSettingsParams) { p.Endpoint = "198.51.100.20:0443" }, func(p *api.HTTPSSettingsParams) { p.Endpoint = "127.0.0.1:443" },
		func(p *api.HTTPSSettingsParams) { p.ServerName = "" }, func(p *api.HTTPSSettingsParams) { p.RequestTarget = "/bad%0d%0a" }, func(p *api.HTTPSSettingsParams) { p.DestinationPolicy = "" }, func(p *api.HTTPSSettingsParams) { p.ExpectedStatus = 0 }, func(p *api.HTTPSSettingsParams) { p.Method = "POST" }, func(p *api.HTTPSSettingsParams) { p.Family = "ipv6" },
	} {
		p := httpsParams()
		edit(&p)
		if _, err := h.SaveHTTPS(context.Background(), p); !errors.Is(err, localapi.ErrInvalidRead) {
			t.Fatal("accepted invalid settings")
		}
	}
	listed, err := h.ListHTTPSSettings(context.Background())
	if err != nil || len(listed.Items) != 0 {
		t.Fatal("invalid input wrote settings")
	}
}
func TestHTTPSCommandsRequireExplicitFieldsAndNoAuthority(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	for _, args := range [][]string{{"https-save"}, {"https-save", "--yes"}, {"https-save", "--server-name", "private.example"}, {"https-retire", "198.51.100.20"}, {"https-retire", "selection." + strings.Repeat("a", 32)}, {"https-list", "--approve"}, {"https-list", "extra"}, {"https-check"}, {"https-plan"}} {
		if err := run(context.Background(), args, out, out); err == nil {
			t.Fatal("accepted invalid command")
		}
	}
	for _, command := range []string{"https-save", "https-list", "https-retire"} {
		if err := run(context.Background(), []string{command, "--help"}, out, out); err != nil {
			t.Fatal(err)
		}
	}
}
func TestHTTPSCLIWorkflowThroughProtectedServer(t *testing.T) {
	h, _, calls := resolverHandler(t)
	dir := t.TempDir()
	server, err := localapi.NewServer(dir, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx) }()
	defer func() { cancel(); _ = server.Close(); <-done }()
	call := func(command string, args ...string) json.RawMessage {
		t.Helper()
		file, err := os.CreateTemp(t.TempDir(), "output")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		full := append([]string{command, "--state-dir", dir}, args...)
		if err := run(ctx, full, file, file); err != nil {
			t.Fatal(command, err)
		}
		raw, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := call("https-save", "--endpoint", "198.51.100.20:443", "--server-name", "Private.Example", "--request-target", "/check?test=1", "--family", "ipv4", "--method", "HEAD", "--expected-status", "204", "--destination-policy", "exact-endpoint")
	var saved api.HTTPSSettingsResult
	if json.Unmarshal(raw, &saved) != nil || len(saved.Items) != 1 || saved.ConsentGranted || *calls != 0 {
		t.Fatal("save failed")
	}
	var listed api.HTTPSSettingsResult
	if json.Unmarshal(call("https-list"), &listed) != nil || len(listed.Items) != 1 || listed.Items[0] != saved.Items[0] {
		t.Fatal("list changed settings")
	}
	var retired api.HTTPSRetireResult
	if json.Unmarshal(call("https-retire", saved.Items[0].SelectionID), &retired) != nil || retired.State != "retired" {
		t.Fatal("retirement failed")
	}
	if json.Unmarshal(call("https-list"), &listed) != nil || len(listed.Items) != 0 || *calls != 0 {
		t.Fatal("retired settings still selected")
	}
}

type httpsUnavailableScopeStore struct{ *storage.Store }

func (s httpsUnavailableScopeStore) ListActiveDeviceWatchScopes(context.Context) ([]domain.NetworkScope, error) {
	return nil, errors.New("scope unavailable")
}
func TestHTTPSRetirementDoesNotRequireCurrentEnrollment(t *testing.T) {
	h, s, _ := resolverHandler(t)
	ctx := context.Background()
	saved, err := h.SaveHTTPS(ctx, httpsParams())
	if err != nil {
		t.Fatal(err)
	}
	h.store = httpsUnavailableScopeStore{s}
	if _, err = h.ListHTTPSSettings(ctx); err == nil {
		t.Fatal("list ignored unavailable scope")
	}
	if _, err = h.SaveHTTPS(ctx, httpsParams()); err == nil {
		t.Fatal("save ignored unavailable scope")
	}
	if _, err = h.RetireHTTPS(ctx, api.HTTPSIDParams{SelectionID: saved.Items[0].SelectionID}); err != nil {
		t.Fatal("scope loss blocked retirement")
	}
}
