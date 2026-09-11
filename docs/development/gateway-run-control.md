# One-shot gateway run control

Related to #14 and #29. This follows the
[route/source metadata preflight](gateway-route-preflight.md) with internal
one-shot orchestration and a real SQLite audit adapter. The later
[experimental native consent session](gateway-consent-session.md) exposes one
connection-bound operation only after explicit macOS controller opt-in. Ordinary
startup and the browser still cannot initiate checks. Existing
`network-quality-plan` responses remain read-only previews and grant no consent.
The [measurement integration](gateway-run-measurements.md) preserves measured
terminal audits. There is no scheduler, automatic probing, generic execution API
or capability lifecycle mutation.

## Authority lives in the controller, not the returned preview

`internal/controller/gatewayrun` owns at most one operation and one outstanding
review. A trusted preflight collaborator returns the controller-resolved scope,
complete interface/prefix binding, selected private IPv4 target, route-associated
source, evidence times, and the existing fixed ICMP budget. The package validates
and copies this selection. A returned review contains a separate defensive copy,
a short-lived process-local ticket, and the evidence-derived expiry time.

The native preview DTO is not accepted as an approval or a run request. Internal
`Run` accepts only that ticket and an explicit one-shot consent signal. Callers
cannot replace the target, source, interface, prefixes, or budget when consuming
it. The ticket has no wire decoder; formatting and JSON redact its value. It is
not stored in SQLite, and persistent run IDs are separate non-authorizing random
identifiers. A future authenticated IPC/CSRF flow must deliberately convey and
bind user consent; this internal boolean is not a replacement for authentication
or an assertion that a real user has consented in the current product.

Missing preflight, executor, or audit collaborators leave the control unavailable.
There is no fallback shell, ping executable, URL, or generic callback supplied by
clients. A concrete ICMP adapter now exists for the candidate sender, but it is
owned dormant by the controller lifecycle. Portable tests use packet-free fakes.
The real route inspector is not promoted to send-binding evidence by this work.

## One-shot state transitions

Preparation performs metadata preflight but writes no audit and grants no consent.
False consent or an unknown ticket cannot start a run. After valid explicit
consent, the ticket is atomically removed before audit or execution admission.
An expired or pre-canceled attempt also burns its ticket but grants no authority.
Retries never restore a consumed review, even when no packets would have been sent.

For an admitted attempt, the sequence is:

1. Commit an `authorized` audit for the reviewed selection.
2. Collect fresh authoritative preflight evidence and compare all reviewed scope,
   source, target and budget fields. Prefix order is canonicalized. Stale cached
   preflight data, changed selections and unavailable sources stop here.
3. Commit an `admitted` audit, then check cancellation, the original review expiry,
   and the operation deadline again **after** that storage I/O.
4. Invoke the narrowly typed executor once, without implicit retries.
5. Validate returned sample provenance, counts, timing and completion semantics.
6. Commit a `finished` audit with a bounded outcome and any valid measurement.

`authorized` records accepted consent; `admitted` records execution admission.
Neither claims a packet was sent. Terminal outcomes are `completed`, `blocked`,
`canceled`, `failed`, or `indeterminate` (executor panic). Completed means only
that a complete validated sample and its terminal audit committed. It does
not mean ICMP succeeded, the gateway is healthy, or the internet is available.
A completed run may have zero replies and unknown RTT. Canceled/failed work may
retain valid partial counts, never an inferred loss percentage.

## Audits and failure recovery

`Store.InsertGatewayRunAudit` appends validated events to the existing durable
`audit_events` table, with its existing quota and audit retention. Unique run/phase
IDs reject duplicate lifecycle rows. Events include a non-authorizing run ID,
controller-selected scope/interface, numeric target/source, fixed profile, approved
selection digest, phase/outcome, timestamp and fixed reason code. They contain no
review ticket, credentials, raw diagnostics, packets or fabricated quality metrics.
Version 2 terminal audits optionally attach the bounded measurement in the same
SQLite write; version 1 remains readable as legacy execution-only evidence.
The selection digest binds canonical prefixes and all fixed budget values without
copying a raw preflight payload into the audit record.

An unconfirmed audit write locks this control instance. Before admission it stops
execution; after execution it prevents reporting confirmed completion or retrying.
Even an error returned after a possible commit is not retried. Existing durable
`authorized`/`admitted` rows may remain without a terminal row after a crash,
clock failure or storage failure: **outcome unknown**, not successful or not sent.
No recovery process replays such runs or reconstructs tickets from audit rows.

Terminal audit cleanup is synchronous and has an independent one-second context,
so caller cancellation does not automatically erase the outcome. There is no
detached worker. A collaborator that ignores cancellation keeps the active
reservation until it returns; no second run is admitted alongside an orphan.
Controller shutdown uses `Shutdown` to close admission and join active
collaborators before closing storage.

## Time and resource bounds

The existing thirty-second review window is evidence-based and half-open. Fresh
revalidation cannot extend the original approval. Wall-clock deadlines are checked
alongside process-local elapsed time; rollback locks the instance instead of
reviving approvals after the clock catches up. Invalid nanosecond timestamps are
rejected before persistence. Expired reviews are discarded lazily without polling.

The control allows one operation and one pending review. It enforces a minimum
sixty seconds between consumed run admissions across **all** targets, including
blocked attempts. A new control begins with a full sixty-second quiet interval,
so restarting cannot reset the limiter to immediate eligibility. Exactly one
control instance must be owned per controller lifetime; this is not a cross-process
or distributed rate limiter. The controller lifecycle owns that single instance. The dedicated native
consent session is available only with explicit experimental startup opt-in;
there is no browser or general-purpose execution endpoint.

Prepare and Run each receive a five-second operation context. Run is additionally
capped by the original review's remaining wall/monotonic lifetime, including the
context passed to the executor; newer preflight evidence cannot extend it. Terminal
audit cleanup may add at most its separate cooperative one-second context. Collaborators
must obey their contexts and OS I/O limits: this package does not forcibly interrupt
kernel calls or certify wire-level rate/byte/receive enforcement. The sender
must independently validate its actual socket source/interface immediately before
EVERY send, enforce the fixed attempt/receive budgets, and stop on cancellation.

## Tests and remaining integration

Portable tests cover explicit consent, ticket replay, parallel duplicate callers,
expiry before and during preflight/audit, defensive copies, every reviewed field,
source changes, canonical prefix ordering, cancellation, panic, audit failure at
each boundary, rollback, startup cooldown, close, missing dependencies and entropy
failure. SQLite integration tests verify durable admission before a packet-free
fake executor, trigger-induced write failures, cancellation cleanup, retention,
duplicate rejection, and persisted audits without restartable authority or new
monitoring history. These are synthetic control-flow tests, not live consent or
probe traffic, and not macOS runtime/hardware evidence.

The [isolated macOS lab](macos-network-lab.md) now drives the real sender through
this coordinator and its adapter; its native-session case also exercises the
opted-in real controller process.
The [controller lifecycle](gateway-controller-lifecycle.md) now owns one dormant
control and joins active work before storage cleanup using `Shutdown`.
Before broad user-facing controls: validate packaged permissions and remaining
hardware gates; add deliberate client presentation; preserve unsupported-platform behavior; and project actual
measurements separately from audited execution state. Enrollment remains distinct
from Device Watch enablement and from permission for an individual active check.

Implementation references: [Go context cancellation and cleanup](https://pkg.go.dev/context)
and [Go wall/monotonic time semantics](https://pkg.go.dev/time#hdr-Monotonic_Clocks).
