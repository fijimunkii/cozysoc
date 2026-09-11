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
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// This is presentation over the existing typed client, not a second executor.
// The terminal implementation is compiled; flags cannot replace it or consent.
type gatewayCheckClient interface {
	CheckGateway(context.Context, string, func(context.Context, api.GatewayCheckReview) (bool, error)) (api.GatewayCheckResult, error)
}

type gatewayCheckTerminal interface {
	Write(context.Context, string) error
	FlushInput() error
	ReadLine(context.Context) (string, error)
	Close() error
}

var errGatewayTerminal = errors.New("gateway check requires normal-user foreground terminal input and output; pipes, redirection and unattended approval are not supported")

func runGatewayCheckCommand(ctx context.Context, args []string, stdout, stderr *os.File) (err error) {
	fs := flag.NewFlagSet("network-quality-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: cozysoc network-quality-check [--state-dir PATH] TARGET_IPV4\nExperimental macOS one-shot ICMP check. Requires a terminal and an explicitly opted-in controller. Default: decline. No --yes, unattended mode or automatic retry.")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("network-quality-check requires one numeric private TARGET_IPV4; flags must precede the target")
	}
	target := fs.Arg(0)
	if err := networkquality.ValidateGatewayPreviewTarget(target); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Refuse noninteractive use before loading controller credentials or dialing.
	terminal, err := openGatewayTerminal(os.Stdin, stdout)
	if err != nil {
		return err
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
	return performGatewayCheck(ctx, localapi.NewClient(dir), terminal, target)
}

func gatewayPromptContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return errGatewayTerminal
	}
	// A suspended process must not accept input past its absolute review expiry,
	// even before its monotonic context timer observes the suspension.
	if !time.Now().Round(0).Before(deadline.Round(0)) {
		return context.DeadlineExceeded
	}
	return nil
}

func performGatewayCheck(ctx context.Context, client gatewayCheckClient, terminal gatewayCheckTerminal, target string) error {
	approved := false
	result, err := client.CheckGateway(ctx, target, func(decisionCtx context.Context, review api.GatewayCheckReview) (bool, error) {
		if err := gatewayPromptContext(decisionCtx); err != nil {
			return false, err
		}
		if err := terminal.Write(decisionCtx, gatewayReviewText(review)); err != nil {
			return false, err
		}
		// Discard type-ahead, including incomplete canonical lines, AFTER the
		// disclosure and BEFORE showing the exact approval prompt. No raw mode.
		if err := terminal.FlushInput(); err != nil {
			return false, err
		}
		prompt := fmt.Sprintf("Type \"check %s\" to authorize ONLY this sample. Anything else declines.\nApproval (default: decline): ", review.Target)
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
		if line != "check "+review.Target+"\n" {
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
		return gatewayCommandError(err, approved)
	}
	outputCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := terminal.Write(outputCtx, gatewayResultText(result)); err != nil {
		return gatewayCommandError(err, approved)
	}
	if result.Outcome != "completed" && result.Outcome != "declined" {
		return errors.New("gateway run did not complete normally; see the measured evidence above; do not automatically retry")
	}
	return nil
}

// Only the typed client passes validated projections here. No raw JSON, challenge,
// ticket, error text or network-derived free-form string is presented.
func gatewayReviewText(r api.GatewayCheckReview) string {
	b := r.Budget
	return fmt.Sprintf(`
EXPERIMENTAL: one-shot local IPv4 ICMP check
Target: %s (gateway role NOT verified)
Source: %s
Interface: %s (index %d)
Enrolled prefixes: %s
Profile: %s
Created: %s
Review expires: %s (not extended by typing or resuming)

Limits: at most %d requests; at least %d ms apart.
Each request: 8-byte ICMP header + %d-byte payload.
Reply window: %d ms; entire sample: at most %d ms.
ICMP request ceiling: %d bytes across this sample.
Concurrency: %d run; at least %d ms between admissions per controller.
The byte ceiling excludes IP/link overhead, neighbor resolution, replies and retransmissions.

Privacy: this traffic can be seen by the target and network infrastructure.
No DNS lookup, external destination, settings change or background monitoring.
Local audits retain addresses, interface, times and counters, not raw packets.
This sample describes this device/interface and selected target only.
An unanswered ICMP check does not prove gateway failure, an internet outage or a security finding.
Same-binding network reuse cannot be distinguished by interface/prefix matching alone.
Evidence is limited to the tested native Terminal context, not packaged permissions,
physical Wi-Fi/NIC behavior or sleep/resume certification. Use only a network you may test.

`, r.Target, r.Source, r.Binding.InterfaceName, r.Binding.InterfaceIndex, strings.Join(r.Binding.Prefixes, ", "), r.Profile,
		r.CreatedAt.UTC().Format(time.RFC3339Nano), r.ExpiresAt.UTC().Format(time.RFC3339Nano),
		b.MaxAttempts, b.MinIntervalMS, b.PayloadBytes, b.AttemptTimeoutMS, b.TotalTimeoutMS,
		b.MaxICMPRequestBytes, b.MaxConcurrentRuns, b.MinRunIntervalMS)
}

func gatewayResultText(r api.GatewayCheckResult) string {
	if r.Outcome == "declined" {
		return "Declined. This command did not authorize a check.\n"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\nRun state: %s (terminal audit confirmed)\nRun reference: %s\n", r.Outcome, r.RunID)
	fmt.Fprintf(&out, "Target: %s; source: %s; interface: %s (index %d)\n", r.Review.Target, r.Review.Source, r.Review.Binding.InterfaceName, r.Review.Binding.InterfaceIndex)
	if r.FailureCode != "" {
		fmt.Fprintf(&out, "Failure code: %s\n", r.FailureCode)
	}
	m := r.Measurement
	if m == nil {
		out.WriteString("Sample: not measured (no usable evidence).\n")
	} else {
		state := "incomplete"
		if m.Complete {
			state = "complete"
		}
		fmt.Fprintf(&out, "Sample: %s\nStarted: %s\n", state, m.StartedAt.UTC().Format(time.RFC3339Nano))
		if m.CompletedAt != nil {
			fmt.Fprintf(&out, "Measurement window ended: %s\n", m.CompletedAt.UTC().Format(time.RFC3339Nano))
		}
		fmt.Fprintf(&out, "Send calls: %d; kernel-accepted requests: %d (not proof of wire egress)\nMatched replies: %d; completed timeouts: %d\n", m.SendCalls, m.AcceptedRequests, m.Replies, m.Timeouts)
		if m.MeanRTTNanoseconds == nil {
			out.WriteString("Mean round-trip time: unknown\n")
		} else {
			fmt.Fprintf(&out, "Mean round-trip time (matched replies only): %s\n", time.Duration(*m.MeanRTTNanoseconds))
		}
		if !m.Complete {
			out.WriteString("Partial counters only: unsent attempts are not timeouts; no loss percentage is inferred.\n")
		}
	}
	out.WriteString("Historical sample, not continuous monitoring or an internet/security verdict.\nUnanswered ICMP may reflect filtering or target behavior. Compare other authorized evidence.\nDo not automatically retry; a new check needs a new review and approval.\n")
	return out.String()
}

func gatewayCommandError(err error, approved bool) error {
	if approved {
		return errors.New("gateway result unavailable after approval; traffic may have been sent and the outcome may be unknown; do not automatically retry")
	}
	message := "gateway check unavailable; no approval was submitted by this command"
	var response *localapi.ResponseError
	if errors.As(err, &response) {
		switch response.Code {
		case "unavailable":
			message = "gateway checks are unavailable; requires a separately started macOS controller with --experimental-gateway-checks"
		case "cooldown":
			message = "controller is in its one-minute startup or run cooldown; no approval was submitted"
		case "busy":
			message = "another review or run is active; no approval was submitted"
		case "precondition_failed":
			message = "current enrollment, interface or route could not be verified; no approval was submitted"
		case "unauthorized":
			message = "controller authentication failed; no approval was submitted"
		case "audit_unconfirmed", "clock_invalid":
			message = "controller safety lock prevents this check; no approval was submitted"
		}
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		message = "review canceled or expired; no approval was submitted by this command"
	}
	return errors.New(message + "; no automatic retry")
}
