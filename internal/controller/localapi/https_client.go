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
	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
)

var errHTTPSInterrupted = errors.New("https exchange interrupted; outcome may be unknown; do not automatically retry")

// CheckHTTPS performs exactly one connection-bound review/decision/result
// exchange. confirm must present target/source/interface/budget/privacy limits,
// default to decline, and honor its context. This is not a generic RPC callback:
// it runs locally and returns only a boolean decision, never execution code.
// Callback input is a copy. No ticket leaves the controller and no failure retries.
// A nil error means the protocol completed, NOT that a run or a probe succeeded:
// callers must inspect Outcome, FailureCode and the optional Measurement.
func (c *Client) CheckHTTPS(parent context.Context, selectionID string, confirm func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error) {
	if c == nil || confirm == nil || !ValidHTTPSSelectionID(selectionID) {
		return api.HTTPSCheckResult{}, errHTTPSProtocol
	}
	if err := parent.Err(); err != nil {
		return api.HTTPSCheckResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 2*requestTimeout+httpsplan.ReviewLifetime+httpsrun.AuditTimeout+time.Second)
	defer cancel()
	secret, err := loadSessionSecret(c.stateDir)
	if err != nil {
		return api.HTTPSCheckResult{}, err
	}
	conn, err := (&net.Dialer{Timeout: requestTimeout}).DialContext(ctx, "unix", c.socket)
	if err != nil {
		return api.HTTPSCheckResult{}, errHTTPSInterrupted
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	peer, err := verifyPeer(conn)
	if err != nil || !peer.Verified || peer.UID != os.Geteuid() {
		return api.HTTPSCheckResult{}, errors.New("https controller identity could not be verified")
	}
	_ = conn.SetDeadline(time.Now().Add(httpsrun.OperationTimeout + time.Second))
	params, _ := json.Marshal(api.HTTPSIDParams{SelectionID: selectionID})
	const id = "https-check"
	if json.NewEncoder(conn).Encode(api.Request{Version: api.Version, ID: id, Method: api.MethodHTTPSCheck, Auth: secret, Params: params}) != nil {
		return api.HTTPSCheckResult{}, errHTTPSInterrupted
	}
	reader := bufio.NewReaderSize(conn, httpsFrameLimit+1)
	raw, err := readHTTPSResponse(reader, id)
	if err != nil {
		return api.HTTPSCheckResult{}, err
	}
	var review api.HTTPSCheckReview
	if decodeStrictJSON(raw, &review) != nil || validateHTTPSReview(review, selectionID, time.Now()) != nil {
		return api.HTTPSCheckResult{}, errHTTPSProtocol
	}
	decisionCtx, stopDecision := context.WithDeadline(ctx, review.ExpiresAt)
	defer stopDecision()
	approve, err := confirm(decisionCtx, cloneHTTPSReview(review))
	if err != nil {
		return api.HTTPSCheckResult{}, err
	}
	if err := decisionCtx.Err(); err != nil {
		return api.HTTPSCheckResult{}, err
	}
	if !time.Now().Before(review.ExpiresAt) {
		return api.HTTPSCheckResult{}, context.DeadlineExceeded
	}
	deadline := minTime(time.Now().Add(httpsrun.OperationTimeout+httpsrun.AuditTimeout+time.Second), review.ExpiresAt.Add(httpsrun.AuditTimeout+time.Second))
	_ = conn.SetDeadline(deadline)
	if json.NewEncoder(conn).Encode(api.HTTPSCheckDecision{Challenge: review.Challenge, Approve: &approve}) != nil {
		return api.HTTPSCheckResult{}, errHTTPSInterrupted
	}
	raw, err = readHTTPSResponse(reader, id)
	if err != nil {
		if ctx.Err() != nil {
			return api.HTTPSCheckResult{}, ctx.Err()
		}
		return api.HTTPSCheckResult{}, err
	}
	var result api.HTTPSCheckResult
	if decodeStrictJSON(raw, &result) != nil || validateHTTPSResult(result, review, time.Now()) != nil {
		return api.HTTPSCheckResult{}, errHTTPSProtocol
	}
	if (approve && result.Outcome == "declined") || (!approve && result.Outcome != "declined") {
		return api.HTTPSCheckResult{}, errHTTPSProtocol
	}
	return result, nil
}

func readHTTPSResponse(reader *bufio.Reader, id string) (json.RawMessage, error) {
	frame, err := httpsFrame(reader)
	if err != nil {
		return nil, errHTTPSInterrupted
	}
	var response api.Response
	if decodeStrictJSON(frame, &response) != nil || response.Version != api.Version || response.ID != id ||
		(response.Error == nil) == (len(response.Result) == 0) {
		return nil, errHTTPSProtocol
	}
	if response.Error != nil {
		code := response.Error.Code
		switch code {
		case "unauthorized", "unavailable", "cooldown", "busy", "review_expired", "invalid_request", "precondition_failed", "audit_unconfirmed", "clock_invalid", "canceled", "execution_failed":
		default:
			code = "unavailable"
		}
		// Do not repeat arbitrary remote diagnostics or auth values in client logs.
		return nil, &ResponseError{Code: code, Message: "https exchange did not produce a confirmed result; do not automatically retry"}
	}
	return response.Result, nil
}
