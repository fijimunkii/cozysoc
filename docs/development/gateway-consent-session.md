# Experimental connection-bound native gateway consent

Related to #14 and #29. This adds the first authenticated native review / explicit
consent / measured-result exchange through the controller-owned coordinator.
It is **off by default**, with no browser route, Run button, automatic check,
scheduler, new dependency, or general-purpose execution API.

## Explicit experimental boundary

On macOS only, an operator may start the controller with:

```text
cozysoc serve --state-dir PATH --experimental-gateway-checks
```

This process-local flag makes the dedicated native protocol available. It is not
persisted, is not passed by `dev`, and grants no approval itself. Normal `serve`,
`dev`, web startup, enrollment, Device Watch enablement and the existing
`network-quality-plan` read remain non-probing. A non-Darwin opt-in fails before
creating a state directory. The ordinary preview still describes only its own
read-only operation; it is not a status query for experimental execution.

The supported experimental execution evidence is the tested macOS runner's
normal-user Terminal launch context, not a packaged app or installed LaunchAgent.
No privacy database, Local Network preference, privilege, firewall or service
registration is modified to make it work. Packaged permission-denied/recovery,
physical NIC/Wi-Fi behavior, real sleep/resume, VPN/Network Extension scenarios
and remaining hardware gates still precede broad user-facing controls. This is a
deliberate opt-in native development boundary, not a claim that those gates passed.

There is not yet an interactive command or browser consent UI. Compiled native
clients use `localapi.Client.CheckGateway` with an explicit confirmation callback.
The next client must visibly present experimental status, selected target/source,
interface and enrolled prefixes, all traffic limits, the review deadline, gateway
role uncertainty and privacy implications before collecting a deliberate choice.
It must default to decline; it must not approve on load or retry automatically.

## One connection, one review, one decision

The first newline-delimited request uses the existing API version and rotating
controller session secret with method `network-quality.gateway-check` and exactly:

```json
{"target":"192.168.50.1"}
```

Both server and typed client require verified OS peer UID matching the local
user. The server also validates its per-controller session secret before touching
scope, preflight or the coordinator. Same UID plus possession of that secret is
not application-code identity or proof of an attentive human; this threat-model
limit is unchanged.

The controller keeps the actual `gatewayrun.Ticket` on the connection handler's
stack. It is never serialized, stored or decoded from client input. A successful
prepare returns a fresh independent 128-bit random challenge and a typed review:
experimental mode, fixed profile, target, source, interface/index, canonical
prefixes, timestamps, bounds and `gateway_role_verified=false`.

Only that same authenticated connection may return the second frame:

```json
{"challenge":"<challenge from this connection>","approve":true}
```

There is no target/source/interface/scope/budget override, ticket field, reusable
bearer approval, separate run endpoint or arbitrary executable. Challenges are
correlation values and have no authority on another connection. Pipelined bytes
already buffered before review or after the decision are rejected; a fresh
unpredictable challenge also prevents a blind precomputed approval.

The consent parser requires exact, unique field names and a real JSON boolean.
Missing/null/string approval, duplicate names (including escaped equivalents),
case-folded names, unknown fields, extra values and oversized frames fail closed.
The existing generic JSON parser is not relaxed or silently changed.

A decline or lost/abandoned review calls `Control.Discard` on precisely its pending
ticket. Discard does not create a consent audit, reset cooldown, affect an admitted
run, close the controller or revoke a newer unrelated review. Expiry, restart and
shutdown never restore an old approval. One outstanding review and one admitted
operation remain controller-wide, not per client or target.

## Cancellation and publication

A consent frame is not a new window: the original review expiry continues to
bound preflight and each send. A connection has bounded authentication, prepare,
review-wait and run/result phases. New frames are at most 8 KiB; the decision
cannot grow past the existing buffered-reader bound. Waiting for consent never
starts the sender. Audit/result delivery receives only bounded cleanup headroom;
that extra time cannot authorize packet sending.

After approval, one connection-owned reader observes EOF, half-close or any extra
bytes and cancels the run. The handler closes and joins that reader before exit.
Server-context cancellation closes active connections; the existing controller
shutdown joins the coordinator before closing SQLite. A disconnect can race an
already-admitted send, so it is not proof that zero packets were sent. No second
connection, retry, detached worker or result cache attempts to finish for a client.

The coordinator still commits authorized/admitted audits before execution and a
validated terminal measurement together with execution state. An unconfirmed
terminal audit returns no result and locks the control. Lost responses remain
unknown to that caller even if the controller committed an audit; clients must
not rerun the action to learn what happened.

`GatewayCheckResult` contains reviewed provenance, the run correlation ID,
execution outcome, bounded failure code and optional complete/partial measurement.
No raw OS/database diagnostic, packet bytes, ICMP nonce, session secret or approval ticket is
returned. Decline has no run ID or measurement. A completed exchange is not a
successful run; a completed run is not proof of a reply or internet availability.
Unsent attempts are not timeouts and absent RTT is not zero. Client validation
checks bounds, fixed-profile consistency and unchanged reviewed provenance before
returning a result. The caller's confirmation callback receives a defensive copy
and cannot retarget the stored approval.

## Test evidence

Portable socket tests use real authenticated Unix connections with synthetic
preflight and packet-free executors. They cover peer/secret checks, strict consent
and request grammar, decline/discard, cross-connection replay, caller mutation,
callback failure, disconnect cancellation, server cancellation, global cooldown,
uncertain terminal audit publication and malformed response projections. Fuzzing
exercises the bounded decision decoder. These tests do not claim live packets.

The isolated macOS matrix adds an eighth required `native-session` case. It builds
and launches the actual `cozysoc serve --experimental-gateway-checks` process in
the lab's Terminal context, enrolls only the job-created feth interface, waits the
full real one-minute startup quiet interval (no test-clock or budget override),
declines once, approves once through the typed authenticated client, and requires
three independently observed requests/replies. It checks post-run cooldown,
Device Watch remaining disabled, orderly process exit, and the matching terminal
measurement in the real SQLite database. Decline adds no gateway audit and the
run adds no observation or coverage records. The checker rejects missing/skipped
cases; ordinary `go test ./...` remains packet-free. The separate ordinary
controller process E2E confirms authenticated checks are unavailable without
opt-in, before any review is offered.

A private child-owning Python monitor runs the real controller. EOF from the test
process, including abrupt parent death, revokes that child's lifetime. The monitor
terminates/joins its own child with bounded kill fallback; the outer lab monitor
waits for its drain confirmation. No PID from a file is used for signaling and no
SSH server, launchd service or broad process kill is added. Portable regressions
cover EOF, timeout and delayed launch after owner death. The longer dedicated lab
timeout accommodates the real startup quiet interval; production budgets do not
change.

Neither #14 nor #29 is complete. Still next: deliberate native/UI presentation,
bounded retained-result reads/assessment, and the remaining packaged/hardware
validation. Do not turn this experimental switch into a default merely because
the controlled native lab passes.

Implementation references: [Go JSON decoding semantics](https://pkg.go.dev/encoding/json)
and [Go connection deadlines and cancellation by close](https://pkg.go.dev/net#Conn).
