package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayicmp"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
)

func TestGatewayResponseValidation(t *testing.T) {
	_, c, _, _, _, _ := sessionFixture(t, nil, nil, false)
	target, _ := parseGatewayAddress("192.168.50.1")
	review, err := c.Prepare(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	wire := projectGatewayReview(review, strings.Repeat("a", 32))
	at := wire.CreatedAt
	zero := time.Duration(0)
	sample := gatewayicmp.Sample{ScopeID: wire.Binding.ScopeID, InterfaceName: wire.Binding.InterfaceName, InterfaceIndex: wire.Binding.InterfaceIndex, Target: target, Source: review.Selection.Source,
		StartedAt: at, CompletedAt: at.Add(3 * time.Second), SendCalls: 3, AcceptedRequests: 3, Replies: 3, Complete: true, MeanRTT: &zero}
	result := projectGatewayResult(wire, gatewayrun.Result{RunID: strings.Repeat("b", 32), Outcome: "completed", Sample: &sample}, nil)
	now := at.Add(4 * time.Second)
	if validateGatewayResult(result, wire, now) != nil {
		t.Fatal("valid result rejected")
	}
	for name, edit := range map[string]func(*api.GatewayCheckResult){
		"schema":                    func(r *api.GatewayCheckResult) { r.SchemaVersion = 2 },
		"run-id":                    func(r *api.GatewayCheckResult) { r.RunID = "<private>" },
		"unknown-outcome":           func(r *api.GatewayCheckResult) { r.Outcome = "internet-up" },
		"changed-target":            func(r *api.GatewayCheckResult) { r.Review.Target = "192.168.50.2" },
		"no-measurement":            func(r *api.GatewayCheckResult) { r.Measurement = nil },
		"invalid-count":             func(r *api.GatewayCheckResult) { r.Measurement.SendCalls = 4 },
		"future":                    func(r *api.GatewayCheckResult) { v := now.Add(time.Second); r.Measurement.CompletedAt = &v },
		"failure-with-complete":     func(r *api.GatewayCheckResult) { r.Outcome = "failed"; r.FailureCode = "execution_failed" },
		"declined-with-measurement": func(r *api.GatewayCheckResult) { r.Outcome = "declined"; r.RunID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(result)
			var changed api.GatewayCheckResult
			_ = json.Unmarshal(raw, &changed)
			edit(&changed)
			if validateGatewayResult(changed, wire, now) == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	for name, edit := range map[string]func(*api.GatewayCheckReview){
		"challenge": func(r *api.GatewayCheckReview) { r.Challenge = "<script>" },
		"source":    func(r *api.GatewayCheckReview) { r.Source = r.Target },
		"budget":    func(r *api.GatewayCheckReview) { r.Budget.MaxAttempts = 4 },
		"expiry":    func(r *api.GatewayCheckReview) { r.ExpiresAt = r.CreatedAt.Add(31 * time.Second) },
		"expired":   func(r *api.GatewayCheckReview) { r.ExpiresAt = now },
		"binding":   func(r *api.GatewayCheckReview) { r.Binding.InterfaceName = "en0;id" },
	} {
		t.Run(name, func(t *testing.T) {
			r := cloneGatewayReview(wire)
			edit(&r)
			if validateGatewayReview(r, wire.Target, now) == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
}

func TestGatewayClientCallbackAbort(t *testing.T) {
	s, c, audit, calls, preflights, _ := sessionFixture(t, nil, nil, false)
	client := NewClient(s.stateDir)
	if _, err := client.CheckGateway(context.Background(), "192.168.50.1", nil); err == nil || preflights.Load() != 0 {
		t.Fatal("nil confirmation performed work")
	}
	cause := errors.New("local user declined presentation")
	_, err := client.CheckGateway(context.Background(), "192.168.50.1", func(context.Context, api.GatewayCheckReview) (bool, error) { return true, cause })
	if err != cause {
		t.Fatal(err)
	}
	waitSession(t, func() bool {
		target, _ := parseGatewayAddress("192.168.50.1")
		r, err := c.Prepare(context.Background(), target)
		if err != nil {
			return false
		}
		c.Discard(r.Ticket)
		return true
	})
	if calls.Load() != 0 || audit.count() != 0 {
		t.Fatal("callback failure approved work")
	}
}

func TestGatewayFramesAndRemoteErrors(t *testing.T) {
	for _, raw := range []string{"", "{}", strings.Repeat("x", gatewayFrameLimit+1) + "\n", "{} true\n", `{"version":1,"id":"other","result":{}}` + "\n", `{"version":1,"id":"gateway-check","result":{},"error":{"code":"private","message":"secret"}}` + "\n"} {
		if _, err := readGatewayResponse(bufio.NewReaderSize(strings.NewReader(raw), gatewayFrameLimit+1), "gateway-check"); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
	raw := `{"version":1,"id":"gateway-check","error":{"code":"arbitrary-private-diagnostic","message":"private secret"}}` + "\n"
	_, err := readGatewayResponse(bufio.NewReader(strings.NewReader(raw)), "gateway-check")
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
		t.Fatal("remote error not sanitized")
	}
}

func FuzzGatewayDecision(f *testing.F) {
	f.Add(`{"challenge":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","approve":true}`)
	f.Add(`{"challenge":"a","approve":false,"approve":true}`)
	f.Fuzz(func(t *testing.T, raw string) {
		var d api.GatewayCheckDecision
		if decodeGatewayObject([]byte(raw), map[string]any{"challenge": &d.Challenge, "approve": &d.Approve}) == nil && d.Approve == nil {
			t.Fatal("missing approval accepted")
		}
	})
}
