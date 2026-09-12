# Native HTTPS consent protocol

Related issues: #14 and #29. The local API has a typed HTTPS session and client,
exercised with synthetic executors over real protected Unix sockets. The product
controller does not implement the session's execution-control interface, so
`network-quality.https-check` returns unavailable before preparing a review.
The `https-check` command implements foreground macOS terminal consent. The
product controller still returns unavailable; no execution opt-in flag or browser
execution route is added. Integrated native HTTPS lab evidence remains required
before product execution.

## Review, decision and result

The session requires verified OS peer identity, the controller session secret,
matching API version and one opaque saved selection ID. Exact, unique JSON keys
reject caller-supplied targets, scope, budgets and approval in the initial request.
The server keeps the coordinator ticket locally. A separate random challenge
correlates one decision on that connection and cannot authorize another session.

The review discloses immutable selection/endpoint/request references, numeric
destination, TLS name, exact request target and bytes, selected source/interface,
route observation/expiry, original review expiry, expected status, every phase and
traffic budget, TLS/HTTP policy and privacy text. The client reconstructs the fixed
plan and compares the entire disclosure before invoking its local confirmation
callback. It rejects altered request bytes, omitted privacy text, relaxed TLS or
traffic limits, noncanonical settings and extended route/review deadlines.
Structural validation alone is not consent or proof of actual socket binding.

The confirmation callback must present the disclosure, default to decline and
honor its deadline. It receives an owned copy and returns only a boolean. The
wire decision requires the exact challenge and an explicit boolean; null, absent,
duplicate, case-varied and extra fields are rejected. Decline or abandonment
discards the review without audit or execution. A consumed approval invokes the
controller's one-shot run coordinator with its original deadline and cooldown.
No failure retries automatically.

After approval, disconnect, half-close or further input cancels execution. The
connection monitor is closed and joined before the handler exits. Cancellation
still permits bounded terminal audit cleanup. Results echo the exact reviewed
disclosure and preserve stage, request state, status and timing. A 503 is a received
response, not expected-status success. A phase timeout differs from cancellation
of the enclosing run. Late incomplete cleanup may survive; a completed response
or timeout beyond the original review expiry is rejected. Uncertain audit results
are suppressed and errors contain normalized codes rather than private diagnostics.

## Bounds and evidence

Incoming requests and decisions retain the 8 KiB frame bound. HTTPS review/result
responses have a separate 64 KiB bound because a 1,024-byte request target appears
both in settings and exact request bytes and can expand under JSON escaping.
Reads use bounded buffers; frames without a newline or beyond the limit fail.
The operation and connection deadlines remain bounded independently of frame size.
These wire disclosures are private local output, not audit payloads.

Tests exercise real Unix authentication, wrong secrets/UIDs/unverified peers,
strict request and decision grammar, approval, decline, abandoned reviews,
cross-connection replay, immutable callback inputs, cooldown, disconnect/server
cancellation and unconfirmed terminal audits. Pure contract tests cover changed
policy/request/privacy, maximum escaped targets, HTTP status semantics and expiry.
The real controller settings process test verifies the session remains unavailable
and its callback is never invoked. These tests do not prove physical networking or integrated native TCP/TLS
execution. The terminal flow below has separate presentation tests.

## Foreground terminal client

`cozysoc https-check [--state-dir PATH] SELECTION_ID` requires one saved HTTPS
selection reference and a normal-user foreground macOS terminal for both input
and output. It reuses the existing checked `/dev/tty` adapter. Noninteractive,
redirected or unsupported terminal use fails before loading credentials or opening
the controller connection. There is no `--yes`, approval flag or unattended mode.
Help states that execution remains unavailable in the current product build.

The client prints the entire validated disclosure, quoting exact request bytes so
CRLF is visible as data. It flushes type-ahead only after disclosure, then asks for
exactly `check SELECTION_ID` followed by a newline. Empty, partial, altered or extra
input declines. Expiration, cancellation and output/input/flush failures prevent
approval. Writing the pre-approval acknowledgement must succeed before the client
returns an approving decision. Failure after approval reports unknown outcome and
consumed approval, never automatic retry.

Result text distinguishes execution outcome from HTTP status expectation, retains
measured zero timing and describes response timing as elapsed connect/TLS/header
time rather than pure network RTT. Missing measurements remain unknown; results
are historical evidence, not current internet availability or security findings.

Synthetic terminal tests exercise exact approval, default decline, ordering of
review/flush/prompt, failures before consent, interruption after approval, every
disclosed budget/policy field, escaped request bytes, status mismatch and zero
timing. CLI tests reject unattended flags/nonterminal use without touching state.
The real HTTPS controller/TCP/TLS/PTY lab gate is still pending; these tests do not
stand in for that end-to-end evidence.
