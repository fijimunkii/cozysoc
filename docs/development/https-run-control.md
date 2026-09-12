# One-shot HTTPS run control and durable audit

Related issues: #14 and #29. `internal/controller/httpsrun` coordinates trusted,
compiled preflight, executor and auditor collaborators. The native TCP candidate
implements its executor contract; SQLite implements its auditor using existing
retention and quota controls. The product controller now owns one instance after
acquiring its protected socket
and drains it before closing storage. No schema migration, native consent endpoint,
browser execution or scheduler is added.

## Admission and lifetime

The controller owner must construct exactly one coordinator and drain it before
closing storage. Startup imposes a full quiet minute. Prepare accepts an opaque
selection ID and returns an immutable review with a random process-local ticket.
It performs metadata preflight only, records no consent and sends no HTTPS probe.
Only one operation or pending review can exist. Discard abandons a matching ticket;
wrong tickets cannot revoke someone else's review.

Run requires explicit one-shot consent. It consumes the ticket and reserves the
one-minute cooldown under a mutex before calling collaborators. It commits an
`authorized` event before re-reading settings/enrollment and route evidence, then
requires the exact reviewed selection and commits `admitted` before execution.
Neither event proves a packet was sent. The executor must still enforce route,
socket binding and traffic bounds. A terminal `finished` event records normalized
evidence or a blocked, canceled, failed or indeterminate outcome.

Fresh preflight never extends the original review deadline. The entire run is
bounded by the earlier original review expiry or eight seconds, including audit
and preflight work. Admission is checked again after audit I/O. Clock rollback
permanently locks the instance without inventing a terminal timestamp. Consumed,
expired or failed tickets never recover authority; restarting creates a fresh
quiet minute and restores no consent from audit rows.

## Evidence and failures

A completed run means a validated HTTP response or a phase timeout plus confirmed
terminal audit. A 503 or redirect remains a received response with its actual
status; it is not silently retried or treated as expected-status success. Connect,
TLS and request-stage timeouts retain their stage and request state. A phase
`DeadlineExceeded` can complete a timeout while the enclosing run remains live;
expiration of the overall deadline cancels the run. Incomplete work stays distinct.

Measurements must match the admitted selection, observer and generated ID. They
must start inside execution and original review bounds and satisfy the normalized
HTTPS contract. Responses/timeouts must finish before original review expiry;
incomplete cleanup may finish afterward without extending send authority. Timing
ends at final-header receipt and must be below eight seconds. Cleanup evidence
retains the normalized thirty-second duration ceiling. Invalid evidence is omitted.
Valid partial evidence may accompany failure or cancellation; that does not claim
clean run completion. Results and audit attachments own independent copies.

Any uncertain audit write locks admission until restart and suppresses the result.
Unknown errors and collaborator panics are normalized without private exception
text. Ordinary execution failure consumes consent and cooldown. Missing terminal
evidence is incomplete history and must never imply success or no traffic.

## Persistence, shutdown and evidence limits

Unique `https-run` rows contain a run/phase ID, immutable configuration references,
observer, normalized stage/request/outcome/status and explicit nanosecond timing.
They contain no endpoint, TLS name, request target, raw response, ticket or private
error text. Configuration records separately preserve attribution. Audit rows
cannot grant authority and create no observations or coverage samples.

Shutdown closes admission, cancels and joins active preflight, execution and
terminal audit before storage closes. An expired shutdown wait never releases
active work early. Terminal audit uses a separate one-second cleanup context after
cancellation; it does not continue sending. Trusted collaborators must honor their
contexts; the coordinator does not detach workers to hide unfinished cleanup.

Unit/race tests exercise replay, concurrent admission, changed selections, expired
route/review evidence, clock reversal, timeout stages, malformed measurements,
panics and shutdown joining. Real SQLite tests verify commit-before-execution,
reopen durability, phase uniqueness, retention and failures at every audit phase.
These are synthetic executor tests, not integrated native HTTPS traffic evidence.
Interactive consent and an integrated native lab remain required before product
execution. #14 and #29 remain open.
