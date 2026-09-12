# Native HTTPS consent protocol

Related issues: #14 and #29. The local API has a typed HTTPS session and client,
exercised with synthetic executors over real protected Unix sockets. The product
controller does not implement the session's execution-control interface, so
`network-quality.https-check` returns unavailable before preparing a review.
There is no CLI check command, browser execution route or experimental enable flag
in this change. Terminal consent and integrated native HTTPS lab evidence remain
required before product execution.

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
and its callback is never invoked. These tests do not prove terminal foreground
consent, physical networking or integrated native TCP/TLS execution.
