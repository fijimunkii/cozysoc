# Gateway run measurement integration

Related to #14 and #29. This joins the existing one-shot coordinator to the
bounded ICMP candidate. A later [controller lifecycle](gateway-controller-lifecycle.md)
owns a dormant-by-default instance. The [native consent session](gateway-consent-session.md)
requires explicit experimental macOS startup opt-in plus one-shot approval. There
is no browser consent flow, scheduler or automatic active check.
Enrollment and Device Watch enablement still do not authorize probe traffic.

## Narrow internal adapter

`gatewayrun.Executor` returns a `gatewayicmp.Sample` and an error rather than an
error alone. `NewICMPExecutor` accepts the concrete candidate; construction opens
nothing and a nil candidate remains unavailable. The adapter preserves the exact
caller context (including the original consumed review deadline), clones the
reviewed binding, and calls `Measure` once. It does not retry, substitute a
transport, extend deadlines, or accept a client-supplied execution configuration.

The coordinator retains its existing explicit one-shot ticket, fresh preflight,
durable authorized/admitted audits, one active operation, one-minute global
cooldown, startup quiet interval, panic handling and fail-closed audit/clock locks.
The [controller-owned lifecycle](gateway-controller-lifecycle.md) now installs one
dormant control per serve lifetime and joins its work before storage cleanup.

## Three evidence states

| Evidence | Meaning |
| --- | --- |
| Absent sample | No usable measurement, such as failure before socket creation or a panic. Never a successful zero-loss check. |
| Incomplete sample | Valid partial send/accept/reply/timeout counts; no mean RTT or inferred loss percentage. Unsent attempts are not timeouts. |
| Complete sample | All three attempt windows finished normally. Zero replies is a completed check with unknown RTT, not proof the gateway or internet failed. |

`Result.Outcome` is execution/audit state; `Result.Sample` is separate measured
evidence. A cancellation observed after a valid complete sample may produce a
canceled execution with that already-complete sample. The fields are not forced
to tell the same story. An executor error with a claimed complete sample is
contradictory and rejected. Socket-cleanup failure may retain the sender's final
window timestamp/counts but must clear completion and mean RTT.

## Validation and publication

Before persistence or return, the coordinator checks exact scope/interface/index,
source and target against the admitted selection. Measurement start must be no
earlier than executor entry and fresh route evidence; completion must not be in
the future or at/beyond the original consumed review expiry. A newer preflight
cannot make evidence at that old expiry acceptable.

Counts are bounded to the fixed three-attempt profile before arithmetic. Kernel
acceptance cannot exceed send calls; there can be at most one uncertain send or
one accepted attempt without a completed reply/timeout outcome. Complete samples
require all three outcomes and a valid completion time. Elapsed-time consistency
and RTT bounds reject impossible aggregates; these checks do not authenticate
the collaborator or prove wire-level spacing. The real sender still enforces
per-send route/socket checks, strict reply matching, receive limits and pacing.

Unknown latency is absent. Measured zero is preserved. Every returned pointer is
copied: executor, auditor and caller do not share mutable measurement fields.
Malformed measurements are discarded and produce a bounded `measurement-invalid`
terminal failure, without raw errors or permission to replay the consumed ticket.

## Durable versioned terminal evidence

Version 2 `gateway-run` payloads add an optional `measurement` to the existing
terminal audit. It carries start/completion times, bounded counters, completion
state and optional `mean_rtt_ns` with explicit units. Scope, interface, target,
source, profile and selection digest remain on the enclosing event. No nonce,
packet bytes, session secret, ticket, arbitrary diagnostic or network-wide verdict
is persisted. Authorization/admission and blocked/indeterminate terminal events
cannot carry measurements. Version 2 completed events require complete evidence.

Version 1 payloads remain accepted only without a measurement and retain their
legacy execution-only interpretation. The outer audit row uses the payload's
version; no database migration or historical rewrite is needed. Both versions
use the existing quota, retention and unique per-run/per-phase row identity.

The terminal execution state and measurement commit in **one existing SQLite
audit insert**, not two independently successful writes. An unconfirmed terminal
write returns no result/sample and locks the control against further work. This
includes an uncertain write that may have committed: it is not retried. A caller
must not publish evidence it did not receive as a confirmed result. Missing
terminal rows after crash/clock/storage failure remain indeterminate, not success.
No restart recovers tickets or repeats the action from a stored measurement.

This is retained audit evidence, not a new measurement-history query API or an
assessment projection. No coverage sample, security finding or quality verdict
is generated as a side effect.

## Tests and remaining work

Portable fixtures exercise all reply counts, unknown versus zero RTT, partial
counts, uncertain sends, no-socket errors, cancellation, deadlines, cleanup failure,
post-completion cancellation, malformed provenance/count/time/latency, old-review
expiry, pointer isolation, panic handling and version compatibility. Real SQLite
tests cover reopen/round-trip/version/retention, no authority recovery, duplicate
rejection and trigger-induced failure at every audit boundary. Fuzz inputs cover
count/time/duration extremes without packets.

The existing isolated macOS lab now executes the real coordinator and adapter
for reply, silence, wrong nonce, cancellation and exact-source loss, with the
independent virtual-link peer still checking packet counts/spacing. Its evidence
remains scoped to the tested runner/Terminal context, not a packaged permission
flow or physical Wi-Fi/NIC certification.

The [experimental native consent/result path](gateway-consent-session.md)
and [interactive command](interactive-gateway-check.md) now use this model.
Next: bounded historical reads/assessment. Packaged permission recovery and remaining hardware tests still
precede broad user-facing controls.
