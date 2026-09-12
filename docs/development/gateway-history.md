# Read-only retained gateway history

Related to #14 and #29. Following the experimental interactive gateway command,
this slice makes a recorded run inspectable without repeating network traffic.
It does not enable the experimental sender, renew consent, change monitoring,
add a browser route, or implement multi-source connectivity diagnosis.

```text
cozysoc network-quality-history [--state-dir PATH]
cozysoc network-quality-history [--state-dir PATH] RUN_ID
```

Both commands print typed JSON through the authenticated controller. They work
without a terminal and with experimental execution disabled, including on the
Linux reference path. They do not start a controller, read interface/route or
neighbor metadata, resolve names, call the coordinator, or send packets. The
run reference is the non-authorizing correlation ID printed by a completed
`network-quality-check` exchange, not a ticket or consent challenge.

## Scope and retention

The native method `network-quality.gateway-history` accepts either no parameters
or exactly one nonempty, lowercase 32-hex-character `run_id`. Caller-selected
scope, target, interface, timestamps, budgets and consent are rejected, including
duplicate or case-variant parameter names. Verified same-UID peer identity and the
existing rotating controller secret precede storage access. No HTTP route exists.

The controller resolves the single active stored enrollment, rereads it after
collection, and refuses a changed/revoked scope. The store independently checks
that scope in its read transaction. Historical interface/source fields are not
revalidated against live OS metadata: this is past evidence, not proof that the
computer is still on that network. Same-binding network reuse remains a limit.
Retired or unrelated scopes cannot be selected via a run ID. An unconfigured list
is explicitly empty/unconfigured; an unavailable exact reference is scoped not-found.

The recent list covers the last 24 hours and returns at most 20 runs, newest
retained audit first, with deterministic ID tie breaking. An exact reference
can retrieve older evidence while retained. Neither path resurrects rows at or
beyond their retention deadline, even when a trusted historical as-of is used.
Reads do not prune, rewrite, extend retention, create an audit, or recover approval.
No migration or new storage table is needed.

## Bounded query work

History uses a separate, controller-owned read-only SQLite pool with at most
one connection. It never opens a transaction on the pinned writer connection,
where concurrent autocommit audit writes could otherwise become part of that
read transaction. Pool waits honor the request context. Normal rollback-journal
snapshot/locking remains in effect; no immutable mode, WAL switch or write-capable
fallback is used. Storage shutdown closes the read pool as well as the writer.

The recent list uses the existing audit-time index to inspect at most 256 audit
rows plus one overflow sentinel in its fixed window. Unrelated and expired audit
rows consume this budget; they are not silently scanned without a limit. Only
bounded gateway payloads (at most 4096 bytes) are materialized. The query does not
scan/group all JSON payloads to construct a history page.

`scan_truncated` means the scan budget was reached. `truncated` means additional
qualifying runs exceeded the 20-run output bound. Either flag prevents treating
the list as exhaustive. A busy unrelated audit stream can produce an empty,
scan-truncated list; use a known run reference instead of rerunning the check.
This tradeoff is explicit, not an indexed per-scope history service claim.

For each selected run, at most three primary-key lookups resolve its authorized,
admitted and finished rows in the same read snapshot. An exact run reference
requires only those three lookups, outside the recent-list window but still
subject to current scope and retention. An older sibling outside the scan/window
can therefore be retained without fabricating an absent lifecycle phase.

Persisted envelopes and payloads must agree on ID, schema version, actor and
original audit time. Exact unique schema fields, provenance/digest consistency,
phase order and measurement chronology are validated. Unsupported, oversized,
ambiguous or contradictory records fail the read without returning a partial
success or copying raw database/payload diagnostics. This is validation of stored
evidence, not cryptographic tamper evidence against a same-user database editor.

## Historical assessment, not current connectivity

Every result is `retained-history`, even immediately after a run. Repeated reads
change the read's `as_of`, never measurement timestamps or an evidence freshness
window. There is deliberately no current/fresh network status or global score.

| Retained evidence | Interpretation |
| --- | --- |
| Authorization/admission but no terminal | Outcome unknown. May be in-flight, interrupted or no longer retained; no cause is invented. |
| Legacy v1 completion | Execution-only evidence, not a measured successful check. |
| Terminal without a sample | No usable measurement, regardless of execution state. |
| Incomplete sample | Preserve partial counts; no inferred reply loss or mean RTT. Unsent requests are not timeouts. |
| Complete sample | Describe all/some/no matched replies at the original measurement time, with limited confidence. |

Authorization/admission/terminal retention flags disclose the available phases
separately. A valid terminal sample can remain readable after earlier phases expire;
missing phases are not recreated. Historical ICMP reply loss is derived only from
complete sample counts. Optional measured zero RTT is retained; no replies means
unknown RTT, not zero. Gateway role remains unverified. No sample establishes an
internet outage, current connectivity, whole-home coverage, or security posture.

A later read may find a terminal commit whose acknowledgement was lost to the
original client. That confirms the currently retained record, not that the old
client received it or that an old approval may be replayed. Reading history never
clears the coordinator's audit lock or authorizes an automatic retry.

## Validation and remaining work

Portable tests cover exact/reference reads, legacy/missing/partial/complete
semantics, scope isolation/revocation, expiry boundaries, old references,
truncation, corrupt envelopes/payloads, phase consistency, defensive copies,
real SQLite reopen/read-only isolation, concurrent terminal audit durability,
authenticated strict reads, cancellation and default-off
process behavior. The existing macOS native-session lab reads the actual run
repeatedly through IPC and the built JSON command before its independent peer
finishes counting; the existing three-request ceiling remains unchanged.

Next: shared frontend history presentation and broader corroborated assessments.
The active check stays experimentally gated. Packaged Local Network permissions,
physical Wi-Fi/NIC behavior and actual sleep/resume still require their separate
evidence; this read feature does not close #14 or #29.
