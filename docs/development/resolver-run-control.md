# One-shot resolver run control and durable audit

Related work: #14 and #29. `internal/controller/resolverrun` coordinates the
selected-resolver review and evidence contracts with trusted, compiled preflight,
executor and auditor collaborators. `storage.Store` implements its auditor using
the existing pinned SQLite writer, audit retention and quota controls. No schema
migration, live sender, controller instance, CLI/browser execution endpoint or
background worker is added. Missing collaborators leave admission unavailable.

## Admission and consent

Construct exactly one control per controller lifetime. Startup imposes a full
quiet minute; a new process never restores tickets from audit rows. Prepare takes
an opaque configured selection ID, performs bounded local metadata preflight and
returns one process-local random ticket plus the immutable plan for explicit
review. Only one operation or pending review can exist. Prepare records no consent
and performs no network probe. A transport must discard a declined or abandoned
review and never expose or persist the ticket in diagnostics.

Run requires the ticket and explicit one-shot consent. It consumes the ticket and
reserves the controller-wide cooldown under a mutex before calling collaborators.
Wrong tickets cannot revoke a different pending review. Consumed, expired,
canceled, failed or indeterminate runs never regain authority or retry silently.

The durable sequence is:

1. `authorized`: consent was consumed; no send is implied.
2. Fresh preflight re-reads configuration/enrollment and route metadata. Route
   evidence must be acquired during this operation, before the source-bound plan
   is constructed. All pinned plan values must match the reviewed selection.
3. `admitted`: durable permission to invoke the narrow trusted executor. The
   executor must still revalidate the actual route/socket source and interface
   before sending, and enforce every traffic/deadline bound itself.
4. `finished`: normalized measurement or an explicit blocked, canceled, failed or
   indeterminate outcome. Missing terminal evidence remains incomplete history.

Audit latency is followed by a fresh clock/cancellation check before execution.
Fresh preflight cannot extend original consent: the entire run remains inside
both the original wall/monotonic review deadline and a five-second operation
ceiling. Clock rollback or an unrepresentable clock permanently locks the control.
A clock failure does not invent a terminal timestamp.

## DNS evidence and failures

A completed run means a validated DNS response or a completed accepted-request
timeout, plus confirmed terminal audit. SERVFAIL, REFUSED, NXDOMAIN, truncation,
referral and NODATA retain their own evidence semantics; they do not become
transport failures or internet-down claims. The selected expectation is preserved.
An uncertain send or incomplete exchange remains incomplete and cannot become a
timeout. A completed timeout requires at least the fixed two-second exchange
window. Classic-UDP response codes are limited to 0–15; matched response timing is
required, including measured zero, and remains below the exchange timeout.

Executor evidence must match the admitted observer, selection and generated
measurement ID, lie inside the execution/original-review window and satisfy the
normalized contract. Invalid evidence is omitted from the result and audit.
Valid partial evidence can accompany execution failure or cancellation. A cleanup
failure can retain a valid response without claiming clean run completion.
Returned samples, terminal audit attachments and sender-owned samples are copied
independently. No result is published until terminal audit is confirmed.

Unknown errors and collaborator panics are normalized without retaining private
exception text. Any uncertain audit, including cancellation during a write, locks
the instance and suppresses its result. Ordinary execution failure consumes
consent and starts cooldown but does not imply a permanent audit fault.

## Persistence, privacy and shutdown

Audit rows use the `resolver-run` kind with a unique run/phase ID. They contain the
profile, immutable resolver/query configuration references, observer and normalized
DNS evidence. They contain no selected endpoint, queried name, raw answer/packet,
ticket or arbitrary diagnostic text. Configuration owners must preserve immutable
reference attribution separately; these rows cannot reconstruct deleted
configuration or establish that a reference was never reused. Persistent
configuration ownership is still required before production integration.

SQLite tests verify commit-before-execution, durable reopen, duplicate rejection,
retention assignment and failure at each phase. The adapter uses the existing
audit retention/quota transaction and adds no observations or coverage samples.
The separate read-only history connection is unchanged. A resolver history reader
and UI are not yet implemented.

Close invalidates pending reviews and cancels active work without releasing its
reservation early. Shutdown joins active preflight, execution and terminal audit
before their storage dependencies may be closed. A timed-out shutdown wait does
not detach work or reopen admission. Terminal audit uses a separate one-second
cleanup context after caller cancellation; it never continues sending.
Collaborators must honor bounded contexts; no goroutine is detached to pretend a
hung collaborator stopped. Do not call Shutdown from a collaborator itself.

Synthetic unit/race and real SQLite tests cover replay/concurrency, changed
selection, stale route evidence, original deadlines, clock rollback, malformed
measurements, uncertain sends, panics, cancellation, shutdown joining and storage
failure. They prove no physical DNS traffic, native routing/socket behavior,
production consent UI or resolver reachability. The live sender, route adapter,
immutable persisted configuration and product lifecycle/consent integration remain
required. #14 and #29 stay open.

The [native route inspector](resolver-route-inspection.md) now produces fresh
source-bound plan selections on macOS. Live DNS sending and product integration
remain pending.
