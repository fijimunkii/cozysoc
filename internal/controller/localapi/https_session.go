package localapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
)

// The product handler does not implement this until native consent is enabled.
type HTTPSCheckHandler interface {
	HTTPSCheckControl() (*httpsrun.Control, error)
}

// Called only after verified OS peer identity, session-secret and API-version
// authentication. Tickets stay on this stack and never enter the wire protocol.
func (s *Server) httpsCheck(parent context.Context, conn net.Conn, reader *bufio.Reader, payload []byte, request api.Request) {
	var exact api.Request
	if !gatewayID.MatchString(request.ID) || len(payload) == 0 || payload[len(payload)-1] != '\n' ||
		decodeGatewayObject(payload, map[string]any{"version": &exact.Version, "id": &exact.ID, "method": &exact.Method, "auth": &exact.Auth, "params": &exact.Params}) != nil {
		s.writeError(conn, "", "invalid_request", "invalid https session request")
		return
	}
	var params api.HTTPSIDParams
	if decodeGatewayObject(exact.Params, map[string]any{"selection_id": &params.SelectionID}) != nil || !ValidHTTPSSelectionID(params.SelectionID) || reader.Buffered() != 0 {
		s.writeError(conn, request.ID, "invalid_request", "https session requires one opaque selection reference")
		return
	}
	handler, ok := s.handler.(HTTPSCheckHandler)
	if !ok {
		s.writeError(conn, request.ID, "unavailable", "experimental native https checks are not enabled")
		return
	}
	control, err := handler.HTTPSCheckControl()
	if err != nil || control == nil {
		s.writeError(conn, request.ID, "unavailable", "experimental native https checks are not enabled")
		return
	}
	ctx, cancel := context.WithTimeout(parent, httpsrun.OperationTimeout+httpsplan.ReviewLifetime+httpsrun.AuditTimeout+time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	review, err := control.Prepare(ctx, params.SelectionID)
	if err != nil {
		s.writeError(conn, request.ID, httpsErrorCode(err), "https review is unavailable; no consent was granted")
		return
	}
	defer control.Discard(review.Ticket)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		s.writeError(conn, request.ID, "unavailable", "https review is unavailable")
		return
	}
	wire := projectHTTPSReview(review, hex.EncodeToString(random[:]))
	if validateHTTPSReview(wire, params.SelectionID, time.Now()) != nil {
		s.writeError(conn, request.ID, "review_expired", "https review expired before delivery")
		return
	}
	if conn.SetDeadline(wire.ExpiresAt) != nil || writeHTTPSResponse(conn, request.ID, wire) != nil {
		return
	}
	frame, err := gatewayFrame(reader)
	var decision api.HTTPSCheckDecision
	if err != nil || decodeGatewayObject(frame, map[string]any{"challenge": &decision.Challenge, "approve": &decision.Approve}) != nil ||
		decision.Approve == nil || subtle.ConstantTimeCompare([]byte(decision.Challenge), []byte(wire.Challenge)) != 1 || reader.Buffered() != 0 {
		s.writeError(conn, request.ID, "invalid_request", "https decision is invalid or expired")
		return
	}
	if !time.Now().Before(wire.ExpiresAt) || ctx.Err() != nil {
		s.writeError(conn, request.ID, "review_expired", "https review expired")
		return
	}
	if !*decision.Approve {
		control.Discard(review.Ticket)
		_ = writeHTTPSResponse(conn, request.ID, api.HTTPSCheckResult{SchemaVersion: 1, Review: wire, Outcome: "declined"})
		return
	}
	// Allow bounded audit/result delivery after measurement expiry. Control.Run
	// independently enforces the ORIGINAL approval deadline for all packet work.
	deadline := minTime(time.Now().Add(httpsrun.OperationTimeout+httpsrun.AuditTimeout+time.Second), wire.ExpiresAt.Add(httpsrun.AuditTimeout+time.Second))
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
		s.writeError(conn, request.ID, httpsErrorCode(runErr), "https outcome could not be confirmed; do not automatically retry")
		return
	}
	output := projectHTTPSResult(wire, result, runErr)
	if validateHTTPSResult(output, wire, time.Now()) != nil {
		s.writeError(conn, request.ID, "unavailable", "https result is unavailable; do not automatically retry")
		return
	}
	_ = writeHTTPSResponse(conn, request.ID, output)
}
