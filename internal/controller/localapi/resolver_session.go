package localapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

var errResolverProtocol = errors.New("invalid resolver consent exchange")

// Returns the installed singleton only under the native experimental opt-in.
type ResolverCheckHandler interface {
	ResolverCheckControl() (*resolverrun.Control, error)
}

func resolverErrorCode(err error) string {
	switch {
	case errors.Is(err, resolverrun.ErrCooldown):
		return "cooldown"
	case errors.Is(err, resolverrun.ErrBusy):
		return "busy"
	case errors.Is(err, resolverrun.ErrReview):
		return "review_expired"
	case errors.Is(err, resolverrun.ErrPreflight):
		return "precondition_failed"
	case errors.Is(err, resolverrun.ErrAudit):
		return "audit_unconfirmed"
	case errors.Is(err, resolverrun.ErrClock):
		return "clock_invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, resolverrun.ErrExecution):
		return "execution_failed"
	default:
		return "unavailable"
	}
}

// Called only after verified OS peer identity, session-secret and API-version
// authentication. Tickets stay on this stack and never enter the wire protocol.
func (s *Server) resolverCheck(parent context.Context, conn net.Conn, reader *bufio.Reader, payload []byte, request api.Request) {
	var exact api.Request
	if !gatewayID.MatchString(request.ID) || len(payload) == 0 || payload[len(payload)-1] != '\n' ||
		decodeGatewayObject(payload, map[string]any{"version": &exact.Version, "id": &exact.ID, "method": &exact.Method, "auth": &exact.Auth, "params": &exact.Params}) != nil {
		s.writeError(conn, "", "invalid_request", "invalid resolver session request")
		return
	}
	var params api.ResolverIDParams
	if decodeGatewayObject(exact.Params, map[string]any{"selection_id": &params.SelectionID}) != nil || !ValidResolverSelectionID(params.SelectionID) || reader.Buffered() != 0 {
		s.writeError(conn, request.ID, "invalid_request", "resolver session requires one opaque selection reference")
		return
	}
	handler, ok := s.handler.(ResolverCheckHandler)
	if !ok {
		s.writeError(conn, request.ID, "unavailable", "experimental native resolver checks are not enabled")
		return
	}
	control, err := handler.ResolverCheckControl()
	if err != nil || control == nil {
		s.writeError(conn, request.ID, "unavailable", "experimental native resolver checks are not enabled")
		return
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout+resolverplan.ReviewLifetime+resolverrun.AuditTimeout+time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	review, err := control.Prepare(ctx, params.SelectionID)
	if err != nil {
		s.writeError(conn, request.ID, resolverErrorCode(err), "resolver review is unavailable; no consent was granted")
		return
	}
	defer control.Discard(review.Ticket)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		s.writeError(conn, request.ID, "unavailable", "resolver review is unavailable")
		return
	}
	wire := projectResolverReview(review, hex.EncodeToString(random[:]))
	if validateResolverReview(wire, params.SelectionID, time.Now()) != nil {
		s.writeError(conn, request.ID, "review_expired", "resolver review expired before delivery")
		return
	}
	if conn.SetDeadline(wire.ExpiresAt) != nil || writeGatewayResponse(conn, request.ID, wire) != nil {
		return
	}
	frame, err := gatewayFrame(reader)
	var decision api.ResolverCheckDecision
	if err != nil || decodeGatewayObject(frame, map[string]any{"challenge": &decision.Challenge, "approve": &decision.Approve}) != nil ||
		decision.Approve == nil || subtle.ConstantTimeCompare([]byte(decision.Challenge), []byte(wire.Challenge)) != 1 || reader.Buffered() != 0 {
		s.writeError(conn, request.ID, "invalid_request", "resolver decision is invalid or expired")
		return
	}
	if !time.Now().Before(wire.ExpiresAt) || ctx.Err() != nil {
		s.writeError(conn, request.ID, "review_expired", "resolver review expired")
		return
	}
	if !*decision.Approve {
		control.Discard(review.Ticket)
		_ = writeGatewayResponse(conn, request.ID, api.ResolverCheckResult{SchemaVersion: 1, Review: wire, Outcome: "declined"})
		return
	}
	// Allow bounded audit/result delivery after measurement expiry. Control.Run
	// independently enforces the ORIGINAL approval deadline for all packet work.
	deadline := minTime(time.Now().Add(resolverrun.OperationTimeout+resolverrun.AuditTimeout+time.Second), wire.ExpiresAt.Add(resolverrun.AuditTimeout+time.Second))
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
		s.writeError(conn, request.ID, resolverErrorCode(runErr), "resolver outcome could not be confirmed; do not automatically retry")
		return
	}
	output := projectResolverResult(wire, result, runErr)
	if validateResolverResult(output, wire, time.Now()) != nil {
		s.writeError(conn, request.ID, "unavailable", "resolver result is unavailable; do not automatically retry")
		return
	}
	_ = writeGatewayResponse(conn, request.ID, output)
}
