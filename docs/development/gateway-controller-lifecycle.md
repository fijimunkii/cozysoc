# Controller-owned gateway run lifecycle

Related to #14 and #29. Following the measured-run integration, `cozysoc serve`
now owns one dormant gateway coordinator and concrete ICMP adapter for its
lifetime. The later [native consent session](gateway-consent-session.md) reuses
this owner only behind explicit experimental macOS startup opt-in. There is no
browser route, Run button, automatic retry or scheduling. Opening the app, enrolling a network,
enabling Device Watch and reading gateway previews still do not send probes.

## Ownership and startup

The API handler has a private `gatewayRunLifecycle`, initialized once by `serve`
**after** the authenticated local server acquires its socket and before readiness
is announced. A duplicate rejected by the existing server cannot initialize a
second coordinator. Web, developer clients and read-only requests never create
or replace this owner. A failed/repeated installation cannot reset the existing
control's startup quiet minute, pending review, cooldown, clock lock or audit lock.

The dormant owner uses the real SQLite auditor, concrete ICMP adapter and native
interface/route readers. Constructors perform no scope lookup, route query,
socket creation or audit write. Other platforms retain the concrete adapters'
unsupported behavior; ownership is not a platform-support claim.

The internal `prepare` and `run` entrypoints share their owner with the gated
connection-bound native protocol adapter. They accept
only the already-typed target or process-local ticket plus explicit consent, and
forward to the same coordinator. Authenticated consent must reuse this owner, never allocate a coordinator per
request or target. The owner is
per controller process, not a machine-wide limiter across arbitrary state dirs.

## Trusted preflight, not preview-derived authority

Preview and internal run preflight share the existing enrolled-interface plan
collector and validated native route evidence. A run never reconstructs authority
from the public preview DTO. The controller reads active enrollment before every
prepare/revalidation, requires one current complete interface/prefix binding,
and checks the selected source/target and route evidence.

It rereads durable enrollment after the route read so a removed/replaced scope
cannot ride on older OS metadata. Comparison uses canonical prefix sets rather
than incidental serialized order. This does not make enrollment and route reads
atomic with changes. Per-send route/socket validation and the explicit consent
boundary remain necessary; same-binding network reuse remains a limitation.

Ordinary previews remain read-only and independent of the coordinator's cooldown.
They reserve no ticket, call no executor and still report execution unavailable
and consent not granted.

## Stop, cancel, then join

`Control.Close` remains a nonblocking cancellation request that also invalidates
pending approval. New `Control.Shutdown(ctx)` closes admission and waits until
active prepare/preflight, execution, validation and terminal audit have returned.
A single mutex-protected drain channel serves all waiters; no waiter goroutine
or detached task is created. Idle/repeated shutdown is safe and immediately done.
Do not call Shutdown synchronously from a collaborator it must wait for.

The wait context limits only waiting. If it expires, the coordinator remains
closed and its active reservation remains held. It cannot be replaced or reopened;
a later Shutdown can finish waiting. Timeout is **not** permission to close its
storage dependencies while a collaborator is still using them.

`serve` invokes shutdown before its server/Device Watch/ingestion/storage cleanup
defers complete, using an uncanceled join context. The compiled collaborators
retain their five-second operation and one-second terminal-audit contexts. A
broken collaborator that ignores cancellation leaves teardown pending; the
process does not claim safe cleanup or close SQLite underneath it. An eventual
forced kill still cannot invent a terminal audit or recover consent. This is
cooperative lifecycle safety, not a hard-kill guarantee for arbitrary code.

SIGINT and SIGTERM now request that same orderly controller shutdown. The log
`controller_stopping` precedes draining; `gateway_runs_drained` records completed
gateway collaborator teardown, not a successful measurement or a durable audit.
A failed terminal audit can be drained while the run still returns ErrAudit and
no sample. Shutdown success means no remaining collaborator, not audit success.

## Validation boundary

Portable regressions cover idle/pending reviews, active preflight, a collaborator
that ignores cancellation, terminal audit ordering, failed audit, multiple
shutdown waiters, timed-out/repeated shutdown and prepare/close races. Handler
fixtures verify single installation, startup cooldown, no implicit collection,
preview/consent separation, scope changes and canonical-prefix comparison.

The existing macOS virtual-link lab adds a seventh required `shutdown` case:
close after the first independently observed echo, require terminal cancellation
before shutdown returns, and keep observing to reject a second echo. It uses the
real coordinator/adapter/sender but remains in the normal-user Terminal context;
it does not prove the installed controller service's packaged permissions.
The process smoke also verifies SIGTERM cleanup, successful exit, secret rotation
and restart with SQLite preserved. Its fresh controller has no active probe.

The [experimental native consent path](gateway-consent-session.md) now has a
real-process isolated-lab case. Still next: deliberate client presentation, bounded
history/assessment reads, and validation of the packaged permission flow. Physical Wi-Fi/NIC behavior, sleep/resume and other hardware gates remain
separate. Neither #14 nor #29 is completed by this ownership slice.
