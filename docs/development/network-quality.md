# Network-quality assessment contract

Related work: #14 and #29. The assessment contract began as a fixture-first
slice, not a completed connectivity-diagnosis feature.

## What exists

`internal/controller/networkquality` validates and assesses bounded, already
collected measurements. `Assess` is a pure function: it has no network, process,
filesystem, controller, storage, coverage, or finding dependencies. The controller's
separate [local-interface read](local-network-quality.md) now supplies real OS
metadata through typed UDS/CLI and browser projections. It adds no background
checks, DNS queries, external requests, or speed tests.

The contract separates local interface state, a selected gateway's ICMP checks,
a selected resolver's DNS checks, and a selected external target's ICMP or HTTPS
checks. These method names describe evidence; they are not executable adapters.
Targets are opaque configuration references, not URLs, addresses, or commands.

Every measurement identifies the observing scope, sensor, interface name/index,
target, method/address family through its target, and measurement start/end time.
The assessment retains the selected evidence ID and times, a bounded conclusion,
limited/unknown confidence, and a plain-language next step. There is deliberately
no global score, `internet-down` verdict, security finding, or coverage state.

## Evidence and uncertainty

| Evidence | Assessment boundary |
| --- | --- |
| Interface reports up/down | Describes this local interface, not internet reachability or other household devices. |
| Gateway does not answer ICMP | Reports an unanswered gateway check; filtering is an alternative to gateway failure. |
| DNS check fails | Reports failure of this selected resolver/query check, not all DNS or an ISP outage. |
| One external target fails | Remains target/protocol/family-specific, never a blanket internet-down claim. |
| Some ICMP probes lack replies | Shows **probe reply loss**, not proof of where actual network packet loss occurred. |
| Some DNS/HTTPS checks fail | Shows partial check failure; these counts are not interpreted as packet loss. |
| Permission denied or unsupported source | Not measured, rather than a measured network failure. |
| Recorded sleep/offline/network-change gap | Not measured; never inferred merely from missing data. |
| No measurement | Unknown, with no invented timestamp, latency, loss, or gap cause. |
| Old measurement | Historical/stale at the exact freshness boundary, even when replayed recently. |

Confidence is about how far the quality conclusion can extend, not a claim that
input evidence is authenticated. This first slice does not corroborate root
causes or assign numerical confidence. In particular, HTTPS success is not
assumed independent of DNS, and IPv4 results are never promoted to IPv6 results.

## Selection and limits

`Snapshot` is an assessment input, **not** a history store or probe schedule.
The latest measurement by completion time is selected for each target, regardless
of input order. A newer explicit gap supersedes an earlier success. A newer
success recovers that check, but does not claim all checks in the history window
succeeded. Target order follows the caller's configured presentation order.

Freshness uses measurement time only. The interval is half-open: evidence is
historical when `CompletedAt + Freshness == AsOf`. Historical evidence keeps its
ID, timestamps, gap reason, and counts for explanation, but does not populate
current latency or reply-loss metrics. Optional measured zero is distinct from
unknown. Output latency values do not alias caller-owned input pointers.

The caller explicitly selects a window of at most 24 hours and freshness between
one second and fifteen minutes. Input contains at most 16 targets and 128
measurements. Measured checks describe at most ten attempts over at most thirty
seconds; explicit gap records may span longer downtime within the input window.
These are initial representation/safety bounds, not measured hardware SLOs,
collection frequency, bandwidth enforcement, or production scheduler guarantees.

Validation rejects unknown layer/method/family/outcome combinations, malformed
or oversized IDs, mismatched observer/scope/interface context, unknown targets,
duplicate target/evidence IDs, simultaneous ambiguous results for one target,
future/inverted/out-of-window timestamps, inconsistent success counts, impossible
latency, and metrics attached to an unmeasured check. Validation errors do not
copy source-controlled identifiers into diagnostics. Callers must bound retrieval
and serialization separately; this function does not ingest untrusted JSON.

## Tests and support claims

The in-package fixture matrices are wholly synthetic; they contain no real host
destinations, household traffic, or credentials. Normal `go test ./...` includes
them without a CI workflow change. They exercise protocol-specific results,
IPv4/IPv6 separation, explicit gaps, empty inputs, freshness boundaries,
replay/order independence, recovery, bounds, invalid inputs, and pointer isolation.

These tests do not certify macOS interface behavior, ICMP permissions, resolver
selection, HTTPS response validation, captive portals, VPN routing, metered
connections, or physical network quality. They do not complete #14 or #29.

## Next integration boundary

The narrow [local-interface evidence producer and native read projection](local-network-quality.md)
now revalidates the enrolled interface/prefix binding before attributing local OS
metadata. A [shared frontend projection](browser-network-quality.md) is also available. Equality of the contract's
observer fields alone remains neither authorization nor proof that the laptop
stayed on the same network.

Active gateway/resolver/external checks must follow separately with explicit
scope/target selection, destination and privacy disclosure, cancellation,
timeouts, retry/rate/concurrency/bandwidth limits, routing/DNS/redirect controls,
and durable bounded history. External dependencies must remain independently
disableable while the app works offline. A real source must supply truthful
protocol success semantics and measurement provenance before UI claims expand.

Do not infer permission to send traffic from Device Watch enrollment or from a
fixture. Do not automatically tune network settings. Preserve the separation of
network quality, operational health, security findings, and monitoring coverage.

## Gateway review boundary

The [native gateway-check preview](gateway-check-preview.md) now reviews one
explicitly selected private IPv4 target and fixed proposed traffic limits. It
sends no probes, grants no execution consent, and does not verify gateway role
or grant send authority. A [macOS route/source preflight](gateway-route-preflight.md)
now checks sampled route/interface-address consistency; actual socket binding and
active execution remain separately gated.
