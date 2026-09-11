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
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

var errGatewayInterrupted = errors.New("gateway exchange interrupted; outcome may be unknown; do not automatically retry")

// CheckGateway performs exactly one connection-bound review/decision/result
// exchange. confirm must present target/source/interface/budget/privacy limits,
// default to decline, and honor its context. This is not a generic RPC callback:
// it runs locally and returns only a boolean decision, never execution code.
// Callback input is a copy. No ticket leaves the controller and no failure retries.
// A nil error means the protocol completed, NOT that a run or a probe succeeded:
// callers must inspect Outcome, FailureCode and the optional Measurement.
func (c *Client) CheckGateway(parent context.Context, target string, confirm func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error) {
	if c == nil || confirm == nil || networkquality.ValidateGatewayPreviewTarget(target) != nil {
		return api.GatewayCheckResult{}, errGatewayProtocol
	}
	if err := parent.Err(); err != nil {
		return api.GatewayCheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 2*requestTimeout+networkquality.GatewayReviewLifetime+gatewayrun.AuditTimeout+time.Second)
	defer cancel()
	secret, err := loadSessionSecret(c.stateDir)
	if err != nil {
		return api.GatewayCheckResult{}, err
	}
	conn, err := (&net.Dialer{Timeout: requestTimeout}).DialContext(ctx, "unix", c.socket)
	if err != nil {
		return api.GatewayCheckResult{}, errGatewayInterrupted
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	peer, err := verifyPeer(conn)
	if err != nil || !peer.Verified || peer.UID != os.Geteuid() {
		return api.GatewayCheckResult{}, errors.New("gateway controller identity could not be verified")
	}
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))
	params, _ := json.Marshal(api.GatewayPlanParams{Target: target})
	const id = "gateway-check"
	if json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: id, Method: api.MethodGatewayCheck, Auth: secret, Params: params}) != nil {
		return api.GatewayCheckResult{}, errGatewayInterrupted
	}
	reader := bufio.NewReaderSize(conn, gatewayFrameLimit+1)
	raw, err := readGatewayResponse(reader, id)
	if err != nil {
		return api.GatewayCheckResult{}, err
	}
	var review api.GatewayCheckReview
	if decodeStrictJSON(raw, &review) != nil || validateGatewayReview(review, target, time.Now()) != nil {
		return api.GatewayCheckResult{}, errGatewayProtocol
	}
	decisionCtx, stopDecision := context.WithDeadline(ctx, review.ExpiresAt)
	defer stopDecision()
	approve, err := confirm(decisionCtx, cloneGatewayReview(review))
	if err != nil {
		return api.GatewayCheckResult{}, err
	}
	if err := decisionCtx.Err(); err != nil {
		return api.GatewayCheckResult{}, err
	}
	if !time.Now().Before(review.ExpiresAt) {
		return api.GatewayCheckResult{}, context.DeadlineExceeded
	}
	deadline := minTime(time.Now().Add(gatewayrun.OperationTimeout+gatewayrun.AuditTimeout+time.Second), review.ExpiresAt.Add(gatewayrun.AuditTimeout+time.Second))
	_ = conn.SetDeadline(deadline)
	if json.NewEncoder(conn).Encode(api.GatewayCheckDecision{Challenge: review.Challenge, Approve: &approve}) != nil {
		return api.GatewayCheckResult{}, errGatewayInterrupted
	}
	raw, err = readGatewayResponse(reader, id)
	if err != nil {
		if ctx.Err() != nil {
			return api.GatewayCheckResult{}, ctx.Err()
		}
		return api.GatewayCheckResult{}, err
	}
	var result api.GatewayCheckResult
	if decodeStrictJSON(raw, &result) != nil || validateGatewayResult(result, review, time.Now()) != nil {
		return api.GatewayCheckResult{}, errGatewayProtocol
	}
	return result, nil
}

func readGatewayResponse(reader *bufio.Reader, id string) (json.RawMessage, error) {
	frame, err := gatewayFrame(reader)
	if err != nil {
		return nil, errGatewayInterrupted
	}
	var response api.Response
	if decodeStrictJSON(frame, &response) != nil || response.Version != api.Version || response.ID != id ||
		(response.Error == nil) == (len(response.Result) == 0) {
		return nil, errGatewayProtocol
	}
	if response.Error != nil {
		code := response.Error.Code
		switch code {
		case "unauthorized", "unavailable", "cooldown", "busy", "review_expired", "invalid_request", "precondition_failed", "audit_unconfirmed", "clock_invalid", "canceled", "execution_failed":
		default:
			code = "unavailable"
		}
		// Do not repeat arbitrary remote diagnostics or auth values in client logs.
		return nil, &ResponseError{Code: code, Message: "gateway exchange did not produce a confirmed result; do not automatically retry"}
	}
	return response.Result, nil
}
