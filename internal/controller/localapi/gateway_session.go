package localapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

const gatewayFrameLimit = 8192

var gatewayID = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)
var gatewayChallenge = regexp.MustCompile(`^[0-9a-f]{32}$`)
var errGatewayProtocol = errors.New("invalid gateway consent exchange")

// GatewayCheckHandler is a compiled collaborator, never caller-supplied code.
// It returns the existing singleton only when explicit experimental native
// execution is enabled. It MUST NOT create a Control per request or target.
type GatewayCheckHandler interface {
	GatewayCheckControl() (*gatewayrun.Control, error)
}

// decodeGatewayObject uses exact, unique names and requires every field. The
// standard v1 JSON decoder's case folding/last-duplicate-wins behavior is not
// appropriate for a consent frame. No generic decoder behavior is changed.
func decodeGatewayObject(raw []byte, fields map[string]any) error {
	if len(raw) == 0 || len(raw) > gatewayFrameLimit || !utf8.Valid(raw) {
		return errGatewayProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errGatewayProtocol
	}
	seen := make(map[string]bool, len(fields))
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] || fields[name] == nil {
			return errGatewayProtocol
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, fields[name]) != nil {
			return errGatewayProtocol
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return errGatewayProtocol
	}
	if d.Decode(new(any)) != io.EOF {
		return errGatewayProtocol
	}
	return nil
}

func gatewayFrame(reader *bufio.Reader) ([]byte, error) {
	frame, err := reader.ReadSlice('\n')
	if err != nil || len(frame) > gatewayFrameLimit {
		return nil, errGatewayProtocol
	}
	return frame, nil
}

func writeGatewayResponse(conn net.Conn, id string, result any) error {
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > gatewayFrameLimit-256 {
		return errGatewayProtocol
	}
	return json.NewEncoder(conn).Encode(api.Response{Version: api.Version, ID: id, Result: raw})
}

func gatewayErrorCode(err error) string {
	switch {
	case errors.Is(err, gatewayrun.ErrCooldown):
		return "cooldown"
	case errors.Is(err, gatewayrun.ErrBusy):
		return "busy"
	case errors.Is(err, gatewayrun.ErrReview):
		return "review_expired"
	case errors.Is(err, gatewayrun.ErrPreflight):
		return "precondition_failed"
	case errors.Is(err, gatewayrun.ErrAudit):
		return "audit_unconfirmed"
	case errors.Is(err, gatewayrun.ErrClock):
		return "clock_invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, gatewayrun.ErrExecution):
		return "execution_failed"
	default:
		return "unavailable"
	}
}

// Called only after verified OS peer identity, session-secret and API-version
// authentication. Tickets stay on this stack and never enter the wire protocol.
func (s *Server) gatewayCheck(parent context.Context, conn net.Conn, reader *bufio.Reader, payload []byte, request api.Request) {
	var exact api.Request
	if !gatewayID.MatchString(request.ID) || len(payload) == 0 || payload[len(payload)-1] != '\n' ||
		decodeGatewayObject(payload, map[string]any{"version": &exact.Version, "id": &exact.ID, "method": &exact.Method, "auth": &exact.Auth, "params": &exact.Params}) != nil {
		s.writeError(conn, "", "invalid_request", "invalid gateway session request")
		return
	}
	var params api.GatewayPlanParams
	if decodeGatewayObject(exact.Params, map[string]any{"target": &params.Target}) != nil || networkquality.ValidateGatewayPreviewTarget(params.Target) != nil || reader.Buffered() != 0 {
		s.writeError(conn, request.ID, "invalid_request", "gateway session requires one numeric private IPv4 target")
		return
	}
	handler, ok := s.handler.(GatewayCheckHandler)
	if !ok {
		s.writeError(conn, request.ID, "unavailable", "experimental native gateway checks are not enabled")
		return
	}
	control, err := handler.GatewayCheckControl()
	if err != nil || control == nil {
		s.writeError(conn, request.ID, "unavailable", "experimental native gateway checks are not enabled")
		return
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout+networkquality.GatewayReviewLifetime+gatewayrun.AuditTimeout+time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	target, _ := parseGatewayAddress(params.Target)
	review, err := control.Prepare(ctx, target)
	if err != nil {
		s.writeError(conn, request.ID, gatewayErrorCode(err), "gateway review is unavailable; no consent was granted")
		return
	}
	defer control.Discard(review.Ticket)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		s.writeError(conn, request.ID, "unavailable", "gateway review is unavailable")
		return
	}
	wire := projectGatewayReview(review, hex.EncodeToString(random[:]))
	if validateGatewayReview(wire, params.Target, time.Now()) != nil {
		s.writeError(conn, request.ID, "review_expired", "gateway review expired before delivery")
		return
	}
	if conn.SetDeadline(wire.ExpiresAt) != nil || writeGatewayResponse(conn, request.ID, wire) != nil {
		return
	}
	frame, err := gatewayFrame(reader)
	var decision api.GatewayCheckDecision
	if err != nil || decodeGatewayObject(frame, map[string]any{"challenge": &decision.Challenge, "approve": &decision.Approve}) != nil ||
		decision.Approve == nil || subtle.ConstantTimeCompare([]byte(decision.Challenge), []byte(wire.Challenge)) != 1 || reader.Buffered() != 0 {
		s.writeError(conn, request.ID, "invalid_request", "gateway decision is invalid or expired")
		return
	}
	if !time.Now().Before(wire.ExpiresAt) || ctx.Err() != nil {
		s.writeError(conn, request.ID, "review_expired", "gateway review expired")
		return
	}
	if !*decision.Approve {
		control.Discard(review.Ticket)
		_ = writeGatewayResponse(conn, request.ID, api.GatewayCheckResult{SchemaVersion: 1, Review: wire, Outcome: "declined"})
		return
	}
	// Allow bounded audit/result delivery after measurement expiry. Control.Run
	// independently enforces the ORIGINAL approval deadline for all packet work.
	deadline := minTime(time.Now().Add(gatewayrun.OperationTimeout+gatewayrun.AuditTimeout+time.Second), wire.ExpiresAt.Add(gatewayrun.AuditTimeout+time.Second))
	if conn.SetDeadline(deadline) != nil {
		return
	}
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	// This one bounded reader belongs to this connection. EOF, half-close or any
	// further frame cancels execution. Close and join it before leaving the handler.
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		_, _ = reader.ReadByte()
		stopRun()
	}()
	defer func() { _ = conn.Close(); <-monitorDone }()
	result, runErr := control.Run(runCtx, review.Ticket, true)
	if result.RunID == "" {
		s.writeError(conn, request.ID, gatewayErrorCode(runErr), "gateway outcome could not be confirmed; do not automatically retry")
		return
	}
	output := projectGatewayResult(wire, result, runErr)
	if validateGatewayResult(output, wire, time.Now()) != nil {
		s.writeError(conn, request.ID, "unavailable", "gateway result is unavailable; do not automatically retry")
		return
	}
	_ = writeGatewayResponse(conn, request.ID, output)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
