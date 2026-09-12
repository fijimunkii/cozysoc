# Retained network-quality diagnosis

Related issues: #14 and #29. The controller now supplies retained local audit
records to the [corroboration assessor](quality-corroboration.md) through:

```sh
cozysoc network-quality-diagnosis [--state-dir PATH]
```

The authenticated native method is `network-quality.diagnosis`. It accepts no
parameters, including scope, run, time, target, policy or approval overrides. The
CLI may run without a foreground terminal and its escaped JSON may be redirected.
The controller requires verified OS peer identity, the session secret and the API
version. Errors never repeat raw audit/database diagnostics. Overview also offers
a manual historical comparison through the typed browser route described below.

## One bounded historical read

The controller selects the sole active enrolled LAN from storage. It reads
gateway, resolver and HTTPS histories in one SQLite snapshot on the existing separate
`mode=ro` history pool, with a one-second total deadline including connection
queueing. The pinned writer and schema are unchanged. Each layer retains its
24-hour list window, 20-run bound, 256 indexed audit-row scan and up to three exact
phase lookups per candidate. The combined worst-case bounds are three times these
individual limits, not an unbounded audit search. All three reads see the same active
scope and retention cutoff. Enrollment is rechecked before publication.

`ReadQualityHistoryWithHTTPS` supplies this shared snapshot. Each layer retains
its own completeness flags; any corrupt included history rejects the entire read.
Empty HTTPS history remains explicit. The older two-layer storage method remains
available, but diagnosis now uses the three-layer method. The native diagnosis
schema is version 2, and the browser projection validates that version before
publishing its minimized shape. Older diagnosis responses fail closed.

The newest retained run in each layer is selected by its last audit timestamp.
Tied latest runs are rejected as ambiguous. The adapter does not search for a
convenient older success or aggregate every saved target into a network verdict.
Any run/scan truncation suppresses comparison and remains explicit. At least two
layers must be available. A newer missing terminal, execution-only
record, incomplete sample or stale sample in any selected layer keeps comparison
unknown while exposing the selected run references and execution
outcomes. Matched DNS and HTTPS responses retained after failed cleanup remain evidence.
Retired resolver or HTTPS configuration does not change its original expectation.
The adapter selects all available latest layers before testing comparability; it
does not choose a convenient two-layer subset around incompatible or stale HTTPS
evidence. An entirely absent layer can still leave a comparable pair.

## Original observation context

The result is always `retained-comparison`. `read_at` and `since` describe the
history read. `assessment_at` is the latest selected audit timestamp, independent
of read time. The adapter assesses original samples at that historical anchor with
a fixed 30-second freshness and completion-skew policy. Re-reading does not make
samples current or repeat a check. Supporting run references and the original
combined evidence interval are returned only when a comparison is available.

The gateway profile supplies IPv4 evidence. Every selected run must match scope,
interface name/index and transport family, independently of a DNS question's A or
AAAA type. DNS/HTTPS pairs can compare over IPv6 when no gateway run is selected.
A selected IPv4 gateway and IPv6 resolver or HTTPS run produce context mismatch;
an older same-family success cannot replace the latest run.

Only complete ICMP samples become request/reply counts. DNS response, error,
timeout, transport failure and expected negative-reply semantics remain intact.
HTTPS retains its method, expected and actual HTTP status, expectation match,
connection/TLS/request stage, request state, original timing and retained phase
flags. HTTP 503 is still a response and may match an explicitly recorded 503
expectation. A TLS failure is distinct from HTTP rejection or a protocol failure.
A response surviving failed cleanup remains evidence, with the failed execution
outcome visible alongside it. Missing or incomplete exchanges remain unknown.

No source address, resolver or HTTPS endpoint, TLS identity, request path, query
name, raw answer or approval material is exposed by diagnosis. Opaque references
identify original immutable configurations. Individual history commands remain
available for more detailed retained evidence; references never restore consent.

The locally owned audit store supplies a stable local-controller observation-device
reference to the assessor; no client may supply another device reference. The
fixed gateway audit sensor reference describes this adapter's source, not a new
hardware measurement. Historical interface fields do not prove an unchanged route
or network. Run audits are not a complete controller sleep/offline/binding-change
timeline; the adapter cannot invent those missing gaps. It does not add current
local-link observations or infer route continuity from
the historical samples. HTTPS contributes only its recorded endpoint/request
evidence, without resolving DNS, following redirects or reading response bodies.

Conclusions keep unknown/limited confidence, evidence and safe next steps. They do
not establish internet availability, a captive portal, an ISP/root-cause diagnosis,
security or monitoring coverage. A DNS error reply is a response, and the selected
ICMP target's gateway role remains unverified. New measurements still require
separate experimental startup opt-in and deliberate one-shot native approval.

## Read-only browser presentation

Overview's **What earlier checks suggest** card reads
`GET /api/network-quality/diagnosis` using the authenticated local browser session.
The route accepts no query or body, uses a bounded controller request and returns
`Cache-Control: no-store`. The browser performs no background/focus polling and
cancels reads on timeout or departure. Demo mode never requests live diagnosis.

The minimized projection includes original timestamps, selected run references,
interface context, execution outcomes, DNS and HTTPS expectations, comparison references
and unknown/limited confidence. It excludes scope identifiers, native prose,
private destinations/query names and approval material. Go and TypeScript validate
the bounded shape, original-time policy, context, evidence references and confidence.
They preserve the authoritative controller interpretation rather than recomputing
raw measurements. The HTTPS detail reuses the retained-history validator; its
run reference, interface, sample times and execution outcome must agree with the
selected comparison record. Fixed UI explanations provide safe next steps for each supported
conclusion. Expandable records separate execution outcomes from measurement results.
No browser execution authority is added.

Tests cover a shared read snapshot across a concurrent writer, read-only transaction
rollback without losing an acknowledged audit, corruption without partial success,
scope/queue bounds, original-time stability, latest unknowns, context mismatches,
DNS/HTTP expectations, TLS failures, two- and three-layer comparisons, protected IPC and hostile parameters. A real controller/CLI
process regression on macOS and Linux uses synthetic audits with active checks
disabled and confirms stable retained interpretation with no observations added.
With the built UI configured, it also launches the real web process and verifies
authenticated reads preserve the native comparison and original timestamps.
Boundary and component tests cover malformed/contradictory results, authentication,
forbidden parameters, cancellation, retries and demo isolation.
This is process/storage evidence, not a physical-network or packaging claim.
