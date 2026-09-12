package main

import (
	"context"
	"encoding/json"
	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsroute"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

type httpsInspectorFunc func(context.Context, httpsroute.Enrollment, httpsplan.Configuration) (httpsroute.Selection, error)

func (f httpsInspectorFunc) Inspect(ctx context.Context, e httpsroute.Enrollment, c httpsplan.Configuration) (httpsroute.Selection, error) {
	return f(ctx, e, c)
}
func httpsReviewHandler(t *testing.T) (*controllerAPIHandler, *storage.Store, *int) {
	h, s, _ := resolverHandler(t)
	calls := new(int)
	h.httpsRouteInspector = httpsInspectorFunc(func(ctx context.Context, e httpsroute.Enrollment, c httpsplan.Configuration) (httpsroute.Selection, error) {
		*calls++
		p, err := httpsplan.New(httpsplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: netip.MustParseAddr("192.0.2.10")}, c, h.now())
		return httpsroute.Selection{Plan: p, RouteObservedAt: h.now(), RouteFreshUntil: h.now().Add(30 * time.Second)}, err
	})
	t.Cleanup(func() { _ = h.httpsRuns.shutdown(context.Background()) })
	return h, s, calls
}
func saveHTTPS(t *testing.T, h *controllerAPIHandler) string {
	t.Helper()
	r, e := h.SaveHTTPS(context.Background(), httpsParams())
	if e != nil {
		t.Fatal(e)
	}
	return r.Items[0].SelectionID
}
func TestHTTPSReviewRechecksSettingsAndScopeAroundRoute(t *testing.T) {
	for _, mode := range []string{"retired", "revoked", "new-scope", "prefix-change", "storage-error", "canceled", "stale-route", "wrong-selection", "extended-route", "expired-route", "clock-backward", "slow-review"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h, s, _ := httpsReviewHandler(t)
			id := saveHTTPS(t, h)
			scopes, err := s.ListActiveDeviceWatchScopes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			store := &resolverScopeOverride{Store: s, scopes: scopes}
			h.store = store
			original := h.httpsRouteInspector
			h.httpsRouteInspector = httpsInspectorFunc(func(ctx context.Context, e httpsroute.Enrollment, c httpsplan.Configuration) (httpsroute.Selection, error) {
				selection, err := original.Inspect(ctx, e, c)
				switch mode {
				case "retired":
					if err := s.RetireHTTPSConfiguration(ctx, id); err != nil {
						t.Fatal(err)
					}
				case "revoked":
					store.scopes = nil
				case "new-scope":
					store.scopes[0].ID = "scope.replacement"
				case "prefix-change":
					store.scopes[0].Metadata = json.RawMessage(`{"device_watch":{"interface_name":"fixture0","interface_index":7,"prefixes":["192.0.3.0/24"]}}`)
				case "storage-error":
					store.fail = true
				case "canceled":
					cancel()
				case "stale-route":
					selection.RouteObservedAt = h.now().Add(-time.Second)
				case "extended-route":
					selection.RouteFreshUntil = selection.RouteFreshUntil.Add(time.Second)
				case "expired-route":
					selection.RouteFreshUntil = h.now()
				case "clock-backward":
					at := h.now().Add(-time.Second)
					h.now = func() time.Time { return at }
				case "slow-review":
					at := h.now().Add(5 * time.Second)
					h.now = func() time.Time { return at }
				case "wrong-selection":
					c.ServerName = "other.example"
					selection.Plan, _ = httpsplan.New(httpsplan.Binding{Observer: e.Observer, Prefixes: e.Prefixes, Source: netip.MustParseAddr("192.0.2.10")}, c, h.now())
				}
				return selection, err
			})
			if selection, _, err := h.collectHTTPSPlan(ctx, id); err == nil || selection.Plan.Current(h.now()) {
				t.Fatal("changed/invalid preflight accepted")
			}
		})
	}
}

func TestHTTPSReviewCLIHasCompleteDisclosureWithoutExecution(t *testing.T) {
	h, s, calls := httpsReviewHandler(t)
	id := saveHTTPS(t, h)
	dir := t.TempDir()
	server, err := localapi.NewServer(dir, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx) }()
	defer func() { cancel(); _ = server.Close(); <-done }()
	file, err := os.CreateTemp(t.TempDir(), "review")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := run(ctx, []string{"https-plan", "--state-dir", dir, id}, file, file); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	var p api.HTTPSPlan
	if json.Unmarshal(raw, &p) != nil || p.Mode != "preview-only" || p.ExecutionAvailable || p.ConsentGranted || p.Configuration.SelectionID != id || *calls != 1 {
		t.Fatal("invalid preview")
	}
	if p.Budget.MaxRequestBytes != len(p.RequestBytes) || p.Budget.MaxConnections != 1 || p.Budget.MaxRequests != 1 || p.Budget.MaxRetries != 0 || p.Budget.MaxTransportReadBytes != 131072 || p.Budget.MaxTransportWriteBytes != 32768 || p.Budget.TotalTimeoutMS != 8000 || p.Budget.MinRunIntervalMS != 60000 {
		t.Fatal("incomplete budget")
	}
	if p.Policy.MinTLSVersion != "TLS 1.2" || p.Policy.MaxTLSVersion != "TLS 1.3" || p.Policy.ALPN != "http/1.1" || !p.Policy.VerifyServerIdentity || p.Policy.UseProxy || p.Policy.ResolveNames || p.Policy.ReadResponseBody || p.Policy.FollowRedirects || len(p.Privacy) != 4 || len(p.Limitations) == 0 || !strings.Contains(p.RequestBytes, "HEAD /check?test=1 HTTP/1.1\r\n") || !strings.Contains(p.RequestBytes, "Host: private.example\r\n") {
		t.Fatal("incomplete policy or request")
	}
	if !p.RouteObservedAt.Equal(h.now()) || !p.RouteFreshUntil.Equal(h.now().Add(30*time.Second)) || len(p.Binding.Prefixes) != 1 || p.Source != "192.0.2.10" {
		t.Fatal("lost route binding")
	}
	if n, err := s.ObservationCount(ctx); err != nil || n != 0 {
		t.Fatal("preview wrote observations")
	}
}
