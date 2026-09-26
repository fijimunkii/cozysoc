package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
)

type diagnosticPreviewTestHandler struct {
	mutationTestHandler
	preview api.DiagnosticPreview
	err     error
}

func (h *diagnosticPreviewTestHandler) DiagnosticsPreview(context.Context) (api.DiagnosticPreview, error) {
	return h.preview, h.err
}

func TestDiagnosticPreviewRoundTripAndStrictParams(t *testing.T) {
	handler := &diagnosticPreviewTestHandler{preview: api.DiagnosticPreview{
		SchemaVersion: 2, GeneratedAt: time.Unix(1_800_000_000, 0).UTC(),
		Storage: api.DiagnosticStorage{ReadState: "current", QuotaState: "pressure", VolumeState: "full"},
	}}
	server := startMutationTestServer(t, handler)
	raw, err := NewClient(server.stateDir).Call(context.Background(), api.MethodDiagnosticsPreview)
	if err != nil {
		t.Fatal(err)
	}
	var got api.DiagnosticPreview
	if err := json.Unmarshal(raw, &got); err != nil || got.SchemaVersion != 2 || got.Storage.QuotaState != "pressure" || got.Storage.VolumeState != "full" {
		t.Fatalf("diagnostic preview = %+v, %v", got, err)
	}
	_, err = NewClient(server.stateDir).CallWithParams(context.Background(), api.MethodDiagnosticsPreview, map[string]any{"file": "private"})
	if err == nil || !strings.Contains(err.Error(), "invalid_request") {
		t.Fatalf("unexpected params = %v", err)
	}
}

func TestDiagnosticPreviewHidesHandlerFailure(t *testing.T) {
	server := startMutationTestServer(t, &diagnosticPreviewTestHandler{err: errors.New("private /path?token=abc")})
	_, err := NewClient(server.stateDir).Call(context.Background(), api.MethodDiagnosticsPreview)
	if err == nil || !strings.Contains(err.Error(), "internal_error") || strings.Contains(err.Error(), "token=abc") {
		t.Fatalf("handler error leaked: %v", err)
	}
}
