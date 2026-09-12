package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type dnsHistoryTestHandler struct {
	testHandler
	calls   atomic.Int32
	bounded atomic.Bool
	err     error
}

func (h *dnsHistoryTestHandler) ResolverHistory(ctx context.Context, p api.ResolverHistoryParams) (api.ResolverHistory, error) {
	h.calls.Add(1)
	_, ok := ctx.Deadline()
	h.bounded.Store(ok)
	return api.ResolverHistory{SchemaVersion: 1, Mode: "retained-history", AsOf: time.Now().UTC(), LookupRunID: p.RunID, Runs: []api.ResolverHistoryRun{}}, h.err
}

func TestResolverHistoryAuthenticatedReadAndStrictParams(t *testing.T) {
	h := &dnsHistoryTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	for _, id := range []string{"", strings.Repeat("a", 32)} {
		var raw json.RawMessage
		var err error
		if id == "" {
			raw, err = client.Call(context.Background(), api.MethodResolverHistory)
		} else {
			raw, err = client.CallWithParams(context.Background(), api.MethodResolverHistory, api.ResolverHistoryParams{RunID: id})
		}
		var result api.ResolverHistory
		if err != nil || json.Unmarshal(raw, &result) != nil || result.Mode != "retained-history" || result.LookupRunID != id {
			t.Fatal("typed read failed", err)
		}
	}
	if h.calls.Load() != 2 || !h.bounded.Load() {
		t.Fatal("history handler not bounded")
	}
	for _, params := range []string{`null`, `{}`, `{"run_id":null}`, `{"run_id":""}`, `{"Run_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, `{"run_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","run_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`, `{"run_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scope_id":"scope.other"}`, `{"target":"192.168.50.1"}`, `{"approve":true}`, `{"run_id":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`} {
		_, err := client.CallWithParams(context.Background(), api.MethodResolverHistory, json.RawMessage(params))
		var e *ResponseError
		if !errors.As(err, &e) || e.Code != "invalid_request" || h.calls.Load() != 2 {
			t.Fatal("invalid history request reached store", err)
		}
	}
}

func TestDNSHistoryCannotBypassPeerOrSecretChecks(t *testing.T) {
	for _, mode := range []string{"secret", "unverified", "wrong-uid"} {
		t.Run(mode, func(t *testing.T) {
			h := &dnsHistoryTestHandler{}
			verify := verifyPeer
			if mode != "secret" {
				verify = func(net.Conn) (PeerIdentity, error) {
					return PeerIdentity{Verified: mode == "wrong-uid", UID: os.Geteuid() + 1}, nil
				}
			}
			dir, err := os.MkdirTemp("", "cz-history-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			s, err := newServer(dir, h, nil, verify)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); _ = s.Serve(ctx) }()
			defer func() { cancel(); <-done }()
			conn, err := net.Dial("unix", s.SocketPath())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			auth := s.secret
			if mode == "secret" {
				auth = "wrong"
			}
			_ = json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: "history", Method: api.MethodResolverHistory, Auth: auth})
			var response api.Response
			if err := json.NewDecoder(conn).Decode(&response); err != nil || response.Error == nil || response.Error.Code != "unauthorized" || h.calls.Load() != 0 {
				t.Fatalf("unauthorized history reached handler: %+v %v", response, err)
			}
		})
	}
}

func TestDNSHistoryErrorsDoNotLeakRawAuditOrDatabaseContent(t *testing.T) {
	for _, cause := range []error{errors.New("private retained payload"), ErrReadTargetNotFound} {
		h := &dnsHistoryTestHandler{err: cause}
		s := startMutationTestServer(t, h)
		_, err := NewClient(s.stateDir).Call(context.Background(), api.MethodResolverHistory)
		var response *ResponseError
		if !errors.As(err, &response) || strings.Contains(err.Error(), "private") {
			t.Fatal("history diagnostic leaked", err)
		}
		want := "internal_error"
		if cause == ErrReadTargetNotFound {
			want = "not_found"
		}
		if response.Code != want {
			t.Fatal(response.Code)
		}
	}
}
