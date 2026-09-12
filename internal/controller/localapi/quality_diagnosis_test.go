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

type diagnosisTestHandler struct {
	testHandler
	calls   atomic.Int32
	bounded atomic.Bool
	err     error
}

func (h *diagnosisTestHandler) QualityDiagnosis(ctx context.Context) (api.QualityDiagnosis, error) {
	h.calls.Add(1)
	_, ok := ctx.Deadline()
	h.bounded.Store(ok)
	return api.QualityDiagnosis{SchemaVersion: 1, Mode: "retained-comparison", ReadAt: time.Now().UTC()}, h.err
}
func TestQualityDiagnosisAuthenticatedReadAndNoParams(t *testing.T) {
	h := &diagnosisTestHandler{}
	s := startMutationTestServer(t, h)
	client := NewClient(s.stateDir)
	raw, err := client.Call(context.Background(), api.MethodQualityDiagnosis)
	var result api.QualityDiagnosis
	if err != nil || json.Unmarshal(raw, &result) != nil || result.Mode != "retained-comparison" || h.calls.Load() != 1 || !h.bounded.Load() {
		t.Fatal("typed bounded read failed", err)
	}
	for _, params := range []string{`null`, `{}`, `{"scope_id":"scope.other"}`, `{"run_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, `{"as_of":"2026-09-12T12:00:00Z"}`, `{"approve":true}`, `{"max_completion_skew_ms":90000}`} {
		_, err := client.CallWithParams(context.Background(), api.MethodQualityDiagnosis, json.RawMessage(params))
		var e *ResponseError
		if !errors.As(err, &e) || e.Code != "invalid_request" || h.calls.Load() != 1 {
			t.Fatal("parameters reached history reader", err)
		}
	}
}

func TestQualityDiagnosisCannotBypassPeerOrSecretChecks(t *testing.T) {
	for _, mode := range []string{"secret", "unverified", "wrong-uid"} {
		t.Run(mode, func(t *testing.T) {
			h := &diagnosisTestHandler{}
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
			_ = json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: "history", Method: api.MethodQualityDiagnosis, Auth: auth})
			var response api.Response
			if err := json.NewDecoder(conn).Decode(&response); err != nil || response.Error == nil || response.Error.Code != "unauthorized" || h.calls.Load() != 0 {
				t.Fatalf("unauthorized history reached handler: %+v %v", response, err)
			}
		})
	}
}

func TestQualityDiagnosisErrorsDoNotLeakRawAuditOrDatabaseContent(t *testing.T) {
	for _, cause := range []error{errors.New("private retained payload"), ErrReadTargetNotFound} {
		h := &diagnosisTestHandler{err: cause}
		s := startMutationTestServer(t, h)
		_, err := NewClient(s.stateDir).Call(context.Background(), api.MethodQualityDiagnosis)
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
