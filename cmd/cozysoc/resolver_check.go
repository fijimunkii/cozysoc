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
type resolverCheckClient interface {
	CheckResolver(context.Context, string, func(context.Context, api.ResolverCheckReview) (bool, error)) (api.ResolverCheckResult, error)
}

func runResolverCheckCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("resolver-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: cozysoc resolver-check [--state-dir PATH] SELECTION_ID\nExperimental macOS one-shot DNS/UDP check. Requires a terminal and an explicitly opted-in controller. Default: decline. No --yes, unattended mode or automatic retry.")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("resolver-check requires one opaque SELECTION_ID; flags must precede the target")
	}
	target := fs.Arg(0)
	if !localapi.ValidResolverSelectionID(target) {
		return errors.New("resolver-check requires a saved selection ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Refuse noninteractive use before loading controller credentials or dialing.
	terminal, err := openGatewayTerminal(os.Stdin, stdout)
	if err != nil {
		return errors.New("resolver-check requires normal-user foreground macOS terminal input and output; no controller connection was opened")
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
	return performResolverCheck(ctx, localapi.NewClient(dir), terminal, target)
}

func performResolverCheck(ctx context.Context, client resolverCheckClient, terminal gatewayCheckTerminal, target string) error {
	approved := false
	result, err := client.CheckResolver(ctx, target, func(decisionCtx context.Context, review api.ResolverCheckReview) (bool, error) {
		if err := gatewayPromptContext(decisionCtx); err != nil {
			return false, err
		}
		if err := terminal.Write(decisionCtx, resolverReviewText(review)); err != nil {
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
		return resolverCommandError(err, approved)
	}
	outputCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := terminal.Write(outputCtx, resolverResultText(result)); err != nil {
		return resolverCommandError(err, approved)
	}
	if result.Outcome != "completed" && result.Outcome != "declined" {
		return errors.New("resolver run did not complete normally; see the measured evidence above; do not automatically retry")
	}
	return nil
}

// Input is validated by the typed client before it reaches the terminal.
func resolverReviewText(r api.ResolverCheckReview) string {
	p, b := r.Settings, r.Budget
	return fmt.Sprintf(`
EXPERIMENTAL: one-shot selected-resolver DNS/UDP check
Selection: %s
Resolver: %s (%s, %s)
Exact question: %s IN %s; expected: %s
Destination policy: %s; outside enrolled prefixes: %t
Source: %s
Interface: %s (index %d); enrolled routable prefixes: %s
Profile: %s
Created: %s
Review expires: %s (typing or resuming does not extend approval)

Limits: %d send call, %d DNS request bytes, %d reply bytes.
Receive ceiling: %d datagrams and %d calls; exchange %d ms; total %d ms.
Concurrency: %d run; at least %d ms between admissions per controller.
No TCP/EDNS fallback, referrals, host-resolver lookup, automatic retry or settings change.
DNS byte limits exclude IP/link overhead, neighbor resolution and server-side traffic.

Privacy: the exact query is visible to the resolver and network infrastructure.
The resolver MAY FORWARD IT UPSTREAM, even when its address is in enrolled prefixes.
Private settings remain in local SQLite. Run audits retain references, interface,
times and normalized DNS outcomes, not raw query names, replies or packets.
A response may be an error such as NXDOMAIN. A timeout does not prove an internet
outage or a security finding. This is one observer and one selected resolver.
Same-binding network reuse cannot be distinguished by interface/prefix matching.
Support is experimental native Terminal context; physical Wi-Fi/NICs, VPNs,
packaged Local Network permissions and sleep/resume are not certified.
Use only a network and query you may test.

`, r.SelectionID, p.Endpoint, p.Family, p.Transport, p.Name, p.QueryType, p.Expect, p.DestinationScope, r.OutsideEnrolledPrefixes, r.Source, r.Binding.InterfaceName, r.Binding.InterfaceIndex, strings.Join(r.Binding.Prefixes, ", "), r.Profile, r.CreatedAt.Format(time.RFC3339Nano), r.ExpiresAt.Format(time.RFC3339Nano), b.MaxSendCalls, b.MaxRequestBytes, b.MaxReplyBytes, b.MaxReceivedDatagrams, b.MaxReceiveCalls, b.ExchangeTimeoutMS, b.TotalTimeoutMS, b.MaxConcurrentRuns, b.MinRunIntervalMS)
}
func resolverResultText(r api.ResolverCheckResult) string {
	if r.Outcome == "declined" {
		return "Declined. No DNS request was authorized.\n"
	}
	text := fmt.Sprintf("Run: %s\nExecution outcome: %s\n", r.RunID, r.Outcome)
	if m := r.Measurement; m != nil {
		text += fmt.Sprintf("Historical sample: %s to %s\nRequest: %s; DNS exchange: %s\n", m.StartedAt.Format(time.RFC3339Nano), m.CompletedAt.Format(time.RFC3339Nano), m.Request, m.Exchange)
		if m.Reply != nil {
			text += fmt.Sprintf("Matched DNS response: RCODE %d; truncated: %t; answer classification: %s\n", m.Reply.RCode, m.Reply.Truncated, m.Reply.Answer)
		}
		if m.ResponseTimeNanoseconds != nil {
			text += fmt.Sprintf("Response time: %s (not necessarily successful resolution)\n", time.Duration(*m.ResponseTimeNanoseconds))
		}
	} else {
		text += "No measurement was confirmed.\n"
	}
	return text + "Historical evidence only. DNS errors/timeouts do not establish an internet outage or a security finding. Do not automatically retry.\n"
}
func resolverCommandError(err error, approved bool) error {
	if approved {
		return errors.New("resolver check outcome may be unknown; a request may have been sent; approval is consumed; do not automatically retry")
	}
	var response *localapi.ResponseError
	if errors.As(err, &response) && response.Code == "unavailable" {
		return errors.New("resolver checks require a separately started macOS controller with --experimental-resolver-checks and an active saved selection")
	}
	return errors.New("resolver review or terminal exchange failed; no approval was submitted; do not automatically retry")
}
