# Retained HTTPS history

Related issues: #14 and #29. `https-history [--state-dir PATH] [RUN_ID]` reads
normalized one-shot HTTPS audits through the authenticated local controller API.
It works with execution disabled and without a live interface or Device Watch.
It performs no route inspection, sends no traffic, writes no audit, and restores
no approval. Output is escaped JSON and may be redirected.

The typed `network-quality.https-history` method accepts no parameters for a
recent list or exactly one lowercase 32-character hexadecimal `run_id` for an
exact lookup. The controller selects the sole active enrollment from storage and
checks it again after the read. Neither form accepts a scope, target, timestamp,
settings override or consent. Retiring HTTPS settings does not hide their audits;
retiring or replacing enrollment cannot reveal another scope's evidence.

Every item remains historical. It retains immutable selection/endpoint/request
references, method, family, expected status, observer/interface, original audit
and measurement times, execution outcome, stage, request acceptance, HTTP status
and optional response time. Zero timing differs from absent timing. Private
endpoint addresses, TLS names, request targets/content, response content, tickets
and challenges are not loaded or exposed.

The normalized HTTPS assessor interprets evidence at the original sample end;
current-state and freshness fields are excluded. HTTP error statuses and redirects
are responses, not packet loss. Expected-status matching is optional and does not
prove body correctness. Connection, TLS, transport and protocol failures remain
distinct from a completed timeout or an incomplete run. Header response time
includes connection/TLS work and is not pure network RTT. A terminal measurement
can remain useful even if later cleanup caused the execution outcome to fail.
No one endpoint result proves internet availability, a captive portal or security.

Missing authorization/admission/terminal records are marked, never reconstructed.
Without a terminal record the outcome is unknown, not no traffic or failure.
Retained evidence cannot prove an earlier client received the result or authorize
an automatic retry. Reading later does not refresh original timestamps.

The recent list covers 24 hours, returns at most 20 runs, and scans at most 256
indexed audit rows including unrelated and expired rows. Both scan and run-limit
truncation are explicit. Each selected run requires at most three primary-key
lookups; exact lookup bypasses only the recent-list window, never retention or
scope checks. A separate bounded read-only SQLite pool supplies a consistent
snapshot with a one-second deadline. Reads cannot absorb or roll back audit writes.

Audits retain their existing schema, retention and quota limits. Strict decoding
rejects oversized payloads, duplicate/unknown/case-varied/null fields, conflicting
phase identities, invalid times and mismatched row envelopes without returning
partial evidence or raw diagnostics. A historical query time cannot resurrect
expired records.

Unit and SQLite tests cover HTTP status/expectation distinctions, failed and
partial measurements, zero timing, missing/expired phases, malformed records,
scan starvation, exact lookup, enrollment changes, cancellation, reopen, literal
file paths and independent read/write ownership. Authenticated API tests reject
scope/target/approval overrides and unverified peers. The required native HTTPS
session reads list/exact/CLI history after configuration retirement and confirms
original timestamps, audit phases and absence of extra monitoring evidence.
The browser exposes a parameterless authenticated list read at
`/api/network-quality/https-history`. Host/origin/session checks precede the
bounded local call. Query strings, bodies, run/target/scope selectors and approval
are rejected. Its Go projection revalidates the latest retained phase and derives
the historical result again at the original time, excluding native prose, reasons,
scope/sensor IDs and private target settings.

The Recent HTTPS checks card loads only on request, with no polling, focus refresh,
execution action or demo-mode network access. Reads have a six-second timeout,
abort on unmount/mode changes and ignore late results. The TypeScript parser
rejects incompatible phases, status/timing/expectation combinations, malformed
references, invalid original times and oversized payloads. Zero timing, false
expectation matches, late incomplete cleanup and missing phases remain explicit.

The card shows HTTP statuses and redirects directly, separately from execution
outcome. It preserves original timestamps, missing timing, phase provenance,
empty/unenrolled states and truncation warnings. Not-sent refers to the HTTP
request, not absence of connection/TLS traffic. Desktop and narrow mobile layouts
are checked with synthetic fixtures. Real-process tests verify authentication,
privacy filtering, inert repeat reads and execution remaining disabled.
Cross-signal corroboration remains separate work.
