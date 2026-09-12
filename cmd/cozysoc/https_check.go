package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
)

// This is presentation over the existing typed client, not a second executor.
// The terminal implementation is compiled; flags cannot replace it or consent.
type httpsCheckClient interface {
	CheckHTTPS(context.Context, string, func(context.Context, api.HTTPSCheckReview) (bool, error)) (api.HTTPSCheckResult, error)
}

func runHTTPSCheckCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("https-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: cozysoc https-check [--state-dir PATH] SELECTION_ID\nExperimental macOS one-shot HTTPS consent client. Requires a foreground terminal. Requires a controller started with --experimental-https-checks. Default: decline. No --yes, unattended mode or automatic retry.")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("https-check requires one opaque SELECTION_ID; flags must precede the target")
	}
	target := fs.Arg(0)
	if !localapi.ValidHTTPSSelectionID(target) {
		return errors.New("https-check requires a saved selection ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Refuse noninteractive use before loading controller credentials or dialing.
	terminal, err := openGatewayTerminal(os.Stdin, stdout)
	if err != nil {
		return errors.New("https-check requires normal-user foreground macOS terminal input and output; no controller connection was opened")
	}
	defer func() {
		if closeErr := terminal.Close(); closeErr != nil && err == nil {
			err = errors.New("terminal cleanup could not be confirmed; do not automatically retry a check")
		}
	}()
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	return performHTTPSCheck(ctx, localapi.NewClient(dir), terminal, target)
}

func performHTTPSCheck(ctx context.Context, client httpsCheckClient, terminal gatewayCheckTerminal, target string) error {
	approved := false
	result, err := client.CheckHTTPS(ctx, target, func(decisionCtx context.Context, review api.HTTPSCheckReview) (bool, error) {
		if err := gatewayPromptContext(decisionCtx); err != nil {
			return false, err
		}
		if err := terminal.Write(decisionCtx, httpsReviewText(review)); err != nil {
			return false, err
		}
		// Discard type-ahead, including incomplete canonical lines, AFTER the
		// disclosure and BEFORE showing the exact approval prompt. No raw mode.
		if err := terminal.FlushInput(); err != nil {
			return false, err
		}
		prompt := fmt.Sprintf("Type \"check %s\" to authorize ONLY this sample. Anything else declines.\nApproval (default: decline): ", review.SelectionID)
		if err := terminal.Write(decisionCtx, prompt); err != nil {
			return false, err
		}
		line, err := terminal.ReadLine(decisionCtx)
		if err != nil {
			return false, err
		}
		if err := gatewayPromptContext(decisionCtx); err != nil {
			return false, err
		}
		if line != "check "+review.SelectionID+"\n" {
			return false, nil
		}
		// An output failure must still fail before consent, not send invisibly.
		if err := terminal.Write(decisionCtx, "Requesting the reviewed one-shot sample; no automatic retry.\n"); err != nil {
			return false, err
		}
		if err := gatewayPromptContext(decisionCtx); err != nil {
			return false, err
		}
		approved = true
		return true, nil
	})
	if err != nil {
		return httpsCommandError(err, approved)
	}
	outputCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := terminal.Write(outputCtx, httpsResultText(result)); err != nil {
		return httpsCommandError(err, approved)
	}
	if result.Outcome != "completed" && result.Outcome != "declined" {
		return errors.New("https run did not complete normally; see the measured evidence above; do not automatically retry")
	}
	return nil
}

func httpsCommandError(err error, approved bool) error {
	if approved {
		return errors.New("https check outcome may be unknown; a request may have been sent; approval is consumed; do not automatically retry")
	}
	var response *localapi.ResponseError
	if errors.As(err, &response) && response.Code == "unavailable" {
		return errors.New("HTTPS checks require a separately started macOS controller with --experimental-https-checks and an active saved selection")
	}
	return errors.New("https review or terminal exchange failed; no approval was submitted; do not automatically retry")
}

// The typed client validates all values before presentation. Quoting the exact
// request makes its CRLF and any sensitive target characters visible as data.
func httpsReviewText(r api.HTTPSCheckReview) string {
	p, b, tls := r.Settings, r.Budget, r.Policy
	return fmt.Sprintf(`
EXPERIMENTAL: one-shot selected HTTPS check
Selection: %s
Endpoint: %s (%s); TLS server identity: %s
Request: %s %s; expected status: %d
Exact request bytes (quoted): %q
Destination policy: %s; outside enrolled prefixes: %t
Source: %s
Interface: %s (index %d); enrolled routable prefixes: %s
Profile: %s
Created: %s
Route observed: %s; route fresh until: %s
Review expires: %s (typing or resuming does not extend approval)

TLS: %s to %s; trust: %s; verify identity: %t; ALPN: %s; HTTP: %s
Fresh connection: %t; client authentication: %t; session resumption: %t; early data: %t
Proxy: %t; name resolution: %t; redirects: %t; response-body reading: %t
Limits: %d connection, %d request, %d retries; %d request bytes; %d response-header bytes.
TLS stream limits: %d read bytes / %d calls; %d write bytes / %d calls.
Deadlines: connect %d ms; TLS %d ms; response headers %d ms; total %d ms.
Concurrency: %d run; at least %d ms between admissions per controller.

Privacy:
%s

Private settings remain in local SQLite. Audits retain immutable references,
interface, times and normalized outcomes, not raw requests, response content or
TLS identities. This profile adds no cookies, authorization headers or request body.
A received response may be an error or redirect. One target failure does not prove
an internet outage or a security finding. This is one observer and one endpoint.
Same-binding network reuse cannot be distinguished by interface/prefix matching.
Physical Wi-Fi/NICs, VPNs, packaged permissions and sleep/resume are not certified.
Use only a network and endpoint you may test.

`, r.SelectionID, p.Endpoint, p.Family, p.ServerName, p.Method, p.RequestTarget, p.ExpectedStatus, r.RequestBytes, p.DestinationPolicy, r.OutsideEnrolledPrefixes, r.Source, r.Binding.InterfaceName, r.Binding.InterfaceIndex, strings.Join(r.Binding.Prefixes, ", "), r.Profile, r.CreatedAt.Format(time.RFC3339Nano), r.RouteObservedAt.Format(time.RFC3339Nano), r.RouteFreshUntil.Format(time.RFC3339Nano), r.ExpiresAt.Format(time.RFC3339Nano), tls.MinTLSVersion, tls.MaxTLSVersion, tls.TrustStore, tls.VerifyServerIdentity, tls.ALPN, tls.HTTPVersion, tls.FreshConnection, tls.ClientAuthentication, tls.SessionResumption, tls.EarlyData, tls.UseProxy, tls.ResolveNames, tls.FollowRedirects, tls.ReadResponseBody, b.MaxConnections, b.MaxRequests, b.MaxRetries, b.MaxRequestBytes, b.MaxResponseHeaderBytes, b.MaxTransportReadBytes, b.MaxTransportReadCalls, b.MaxTransportWriteBytes, b.MaxTransportWriteCalls, b.ConnectTimeoutMS, b.TLSHandshakeTimeoutMS, b.ResponseHeaderTimeoutMS, b.TotalTimeoutMS, b.MaxConcurrentRuns, b.MinRunIntervalMS, strings.Join(r.Privacy, "\n"))
}

func httpsResultText(r api.HTTPSCheckResult) string {
	if r.Outcome == "declined" {
		return "Declined. No HTTPS request was authorized.\n"
	}
	text := fmt.Sprintf("Run: %s\nExecution outcome: %s\n", r.RunID, r.Outcome)
	if m := r.Measurement; m != nil {
		text += fmt.Sprintf("Historical sample: %s to %s\nStage: %s; request: %s; HTTPS exchange: %s\n", m.StartedAt.Format(time.RFC3339Nano), m.CompletedAt.Format(time.RFC3339Nano), m.Stage, m.Request, m.Exchange)
		if m.StatusCode != 0 {
			text += fmt.Sprintf("Received HTTP status: %d; expected: %d; matches expectation: %t\n", m.StatusCode, r.Review.Settings.ExpectedStatus, m.StatusCode == r.Review.Settings.ExpectedStatus)
		}
		if m.ResponseTimeNanoseconds != nil {
			text += fmt.Sprintf("Time to final response header: %s (includes connect/TLS; not pure network RTT)\n", time.Duration(*m.ResponseTimeNanoseconds))
		}
	} else {
		text += "No measurement was confirmed.\n"
	}
	return text + "Historical evidence only. A single endpoint response or timeout does not establish internet availability or security. Do not automatically retry.\n"
}
