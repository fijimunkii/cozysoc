package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

var errResolverInterrupted = errors.New("resolver exchange interrupted; outcome may be unknown; do not automatically retry")

// CheckResolver performs exactly one connection-bound review/decision/result
// exchange. confirm must present target/source/interface/budget/privacy limits,
// default to decline, and honor its context. This is not a generic RPC callback:
// it runs locally and returns only a boolean decision, never execution code.
// Callback input is a copy. No ticket leaves the controller and no failure retries.
// A nil error means the protocol completed, NOT that a run or a probe succeeded:
// callers must inspect Outcome, FailureCode and the optional Measurement.
func (c *Client) CheckResolver(parent context.Context, selectionID string, confirm func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error) {
	if c == nil || confirm == nil || !ValidResolverSelectionID(selectionID) {
		return api.ResolverCheckResult{}, errResolverProtocol
	}
	if err := parent.Err(); err != nil {
		return api.ResolverCheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 2*requestTimeout+resolverplan.ReviewLifetime+resolverrun.AuditTimeout+time.Second)
	defer cancel()
	secret, err := loadSessionSecret(c.stateDir)
	if err != nil {
		return api.ResolverCheckResult{}, err
	}
	conn, err := (&net.Dialer{Timeout: requestTimeout}).DialContext(ctx, "unix", c.socket)
	if err != nil {
		return api.ResolverCheckResult{}, errResolverInterrupted
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	peer, err := verifyPeer(conn)
	if err != nil || !peer.Verified || peer.UID != os.Geteuid() {
		return api.ResolverCheckResult{}, errors.New("resolver controller identity could not be verified")
	}
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))
	params, _ := json.Marshal(api.ResolverIDParams{SelectionID: selectionID})
	const id = "resolver-check"
	if json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: id, Method: api.MethodResolverCheck, Auth: secret, Params: params}) != nil {
		return api.ResolverCheckResult{}, errResolverInterrupted
	}
	reader := bufio.NewReaderSize(conn, gatewayFrameLimit+1)
	raw, err := readResolverResponse(reader, id)
	if err != nil {
		return api.ResolverCheckResult{}, err
	}
	var review api.ResolverCheckReview
	if decodeStrictJSON(raw, &review) != nil || validateResolverReview(review, selectionID, time.Now()) != nil {
		return api.ResolverCheckResult{}, errResolverProtocol
	}
	decisionCtx, stopDecision := context.WithDeadline(ctx, review.ExpiresAt)
	defer stopDecision()
	approve, err := confirm(decisionCtx, cloneResolverReview(review))
	if err != nil {
		return api.ResolverCheckResult{}, err
	}
	if err := decisionCtx.Err(); err != nil {
		return api.ResolverCheckResult{}, err
	}
	if !time.Now().Before(review.ExpiresAt) {
		return api.ResolverCheckResult{}, context.DeadlineExceeded
	}
	deadline := minTime(time.Now().Add(resolverrun.OperationTimeout+resolverrun.AuditTimeout+time.Second), review.ExpiresAt.Add(resolverrun.AuditTimeout+time.Second))
	_ = conn.SetDeadline(deadline)
	if json.NewEncoder(conn).Encode(api.ResolverCheckDecision{Challenge: review.Challenge, Approve: &approve}) != nil {
		return api.ResolverCheckResult{}, errResolverInterrupted
	}
	raw, err = readResolverResponse(reader, id)
	if err != nil {
		if ctx.Err() != nil {
			return api.ResolverCheckResult{}, ctx.Err()
		}
		return api.ResolverCheckResult{}, err
	}
	var result api.ResolverCheckResult
	if decodeStrictJSON(raw, &result) != nil || validateResolverResult(result, review, time.Now()) != nil {
		return api.ResolverCheckResult{}, errResolverProtocol
	}
	if (approve && result.Outcome == "declined") || (!approve && result.Outcome != "declined") {
		return api.ResolverCheckResult{}, errResolverProtocol
	}
	return result, nil
}

func readResolverResponse(reader *bufio.Reader, id string) (json.RawMessage, error) {
	frame, err := gatewayFrame(reader)
	if err != nil {
		return nil, errResolverInterrupted
	}
	var response api.Response
	if decodeStrictJSON(frame, &response) != nil || response.Version != api.Version || response.ID != id ||
		(response.Error == nil) == (len(response.Result) == 0) {
		return nil, errResolverProtocol
	}
	if response.Error != nil {
		code := response.Error.Code
		switch code {
		case "unauthorized", "unavailable", "cooldown", "busy", "review_expired", "invalid_request", "precondition_failed", "audit_unconfirmed", "clock_invalid", "canceled", "execution_failed":
		default:
			code = "unavailable"
		}
		// Do not repeat arbitrary remote diagnostics or auth values in client logs.
		return nil, &ResponseError{Code: code, Message: "resolver exchange did not produce a confirmed result; do not automatically retry"}
	}
	return response.Result, nil
}
