# Retained selected-resolver history

Related issues: #14 and #29. Native history reads the normalized audits left by
one-shot resolver checks. It works without resolver execution opt-in, Device
Watch enablement or a live network interface. It sends no DNS or other probe,
reads no route/neighbor metadata, creates no ticket or audit, and never restores
approval after an uncertain response.

```sh
cozysoc resolver-history
cozysoc resolver-history RUN_ID
```

Both commands accept `--state-dir PATH` before the optional run reference. Output
is escaped JSON and may be redirected. The authenticated native method
`network-quality.resolver-history` accepts no parameters for a recent list, or
exactly one lowercase 32-character hexadecimal `run_id`. OS peer verification,
rotating session-secret and API-version checks precede the read. The browser also exposes a list-only, authenticated read as described below.

## Historical semantics

Every result remains historical, even if read immediately after the check. The
original measurement and audit timestamps are retained. List `as_of`/`since` are
query boundaries, not refreshed measurement times. Execution outcome stays
separate from DNS exchange and query expectation: a completed run can contain an
expected or unexpected NXDOMAIN, another DNS error, or an accepted timeout. A
matched response retained after failed cleanup stays evidence with failed execution.
Response time is optional nanoseconds, with zero distinct from missing; it is not
successful-lookup latency or packet loss.

Each run contains immutable selection/resolver/query references, transport family,
question type and expectation, observing scope/sensor/interface, retained phase
flags, and optional normalized measurement. It does not load private endpoint/name
settings, raw DNS answers, tickets or challenges. Retiring configuration does not
hide historical evidence or reinterpret its original expectation. The active
network enrollment still scopes the read; retiring/replacing that enrollment
cannot expose a different scope's records.

The historical explanation uses the existing normalized resolver assessor at the
original sample end. Its current-state/freshness fields are never exposed. Answers,
NXDOMAIN, NODATA, refusal, SERVFAIL, format errors, unsupported operations, other
response codes, referrals, truncation, unclassified responses, incomplete work and
timeouts remain distinct. Expected-answer matching is optional and says nothing
about current DNS health, address reachability, DNSSEC or security posture.

Missing authorization/admission/terminal phases are reported, not reconstructed.
They may reflect retention, interruption or an in-flight operation. No retained
terminal means unknown outcome, not no traffic or failure. A retained terminal
can remain useful after earlier phases expire. It cannot prove that an earlier
client received its result or authorize retrying the run.

## Bounds and storage ownership

The recent list covers 24 hours, returns at most 20 runs, and examines at most 256
indexed audit rows, including unrelated and expired rows. It reports both run-limit
and scan-limit truncation instead of representing a starved scan as exhaustive.
Each selected run resolves at most three exact phase keys, including retained
siblings outside the list window. An exact reference bypasses the list window,
not retention or scope checks. Backdating `as_of` cannot resurrect expired rows.

The read has a one-second deadline and a consistent snapshot on the existing
separate `mode=ro`, single-connection history pool. The pinned writer is unchanged.
SQL bounds materialized envelope fields and resolver payloads (4096 bytes), avoids
loading unrelated payloads, and uses the existing timestamp/primary-key indexes.
Exact JSON decoding rejects duplicate, case-variant, unknown, null and unsupported
schema fields, including nested selection, observer and reply records. Envelope
identity, actor, schema and timestamps must agree with decoded events; all retained
siblings must agree on configuration/observer and phase ordering. Corruption never
returns partial success or repeats raw persisted data in API errors.

Tests cover DNS distinctions, stable history across read times/restart, defensive
copies, malformed records, missing/expired phases, old exact references, unrelated
scan starvation, limits, scoped access and read/write snapshot isolation. The
required native resolver-session lab reads list/exact/CLI history after retiring
its saved selection while the independent peer still counts packets. It must
retain original response evidence and finish with exactly one observed DNS query.
These checks do not certify physical NICs, packaged permissions or whole-home
coverage; #14 and #29 remain open.

## Shared browser history

Overview's **Recent DNS checks** card reads `GET /api/network-quality/resolver-history`
only when requested. It accepts no query/body, scope selector, settings, run command
or approval. Host/origin/session checks precede a bounded local controller call.
The Go projection revalidates the latest retained phase with the resolver history
validator and derives the historical result again at the original sample end.
It excludes native prose, failure reasons, scope/sensor IDs and all private query
settings. Immutable references, interface, family, question type and recorded
expectation remain available to correlate a historical check.

The TypeScript parser requires bounded fields, valid original times, compatible
execution/result/response-code combinations and correct optional expectation
matching. Zero response time and an explicit false match survive projection.
The card distinguishes NXDOMAIN, NODATA, DNS error replies, truncation, referrals,
transport failures, timeouts, incomplete work and missing final records. A matched
reply remains visible after failed cleanup; execution completion never means
successful resolution. Confidence and safe next steps stay limited to historical
evidence and never label the network healthy, secure or down from this one check.

Refreshing re-reads the retained list without polling, sending traffic, refreshing
a sample's age or granting approval. Both list/scan truncation and missing phases
are explicit. Unenrolled/empty states do not invent evidence; bounded read failures
stay in the card. Demo mode sends no live request, and leaving the live card aborts
its request and discards late results. Go boundary, TypeScript/parser, component,
and Unix process tests cover this path with synthetic evidence and active checks
disabled. Browser history shares the existing read-only SQLite pool and does not
change storage or the native send/consent path.
