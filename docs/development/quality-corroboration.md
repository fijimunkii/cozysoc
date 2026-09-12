# Comparing network-quality evidence

Related issues: #14 and #29. `networkquality.Corroborate` is the shared, pure
assessment boundary for comparing already-collected local-link, selected ICMP,
normalized DNS and selected external-target evidence. It performs no I/O, creates
no checks, and grants no consent. The [retained-history adapter](retained-quality-diagnosis.md) now supplies a
controller-owned gateway, resolver and HTTPS evidence through native and read-only
browser views. It selects the latest run in each available layer and requires all
selected samples to be usable and comparable, with at least two layers present.

## Observation context

Both collector snapshots must identify the same observing device, enrolled scope,
interface name/index, assessment time, evidence window and freshness policy.
Sensor references stay distinct and are preserved; the trusted caller must supply
truthful device references from the evidence owner. Matching tokens do not verify
provenance, topology or authorization. History adapters must not invent missing
location/binding attribution or combine different controllers just because their
interface names match.

Each call selects IPv4 or IPv6 transport explicitly. DNS A/AAAA question type is
independent of transport family. Interface-state evidence is family-neutral;
other-family check results are excluded. Explicit runtime sleep/offline/binding
gaps concern the shared observation point and can invalidate either family. DNS must use the specialized resolver
contract so negative/error replies, query expectations, uncertain requests and
incomplete exchanges cannot be flattened into generic success/failure counts.

Inputs retain the existing 24-hour window, 16 total targets/selections and 128 total
measurements, with freshness between one second and fifteen minutes. Completion
skew is explicit and bounded from one to thirty seconds. Existing per-check
validation rejects impossible outcomes, duplicate identities, tied latest samples,
invalid times and untrusted enum values before any result is published. The result
is deterministic and owns its optional expectation/configuration values.

## Selection and discontinuities

The latest sample for each selected target/query is chosen first. Stale, absent,
not-measured and incomplete samples remain visible but cannot support a comparison.
A newer gap supersedes an older success; the assessor never searches backward for
a convenient result. At least two check layers must supply comparable measurements.
Multiple queries or targets within one layer do not become cross-layer evidence.

All compared completion times must fit the configured skew, including the exact
boundary. Original start/end times are retained and the report gives the full
compared evidence interval. Nearby completions do not imply simultaneous samples,
the same route or independent failures. If the latest selected measurements are
too far apart, no subset is cherry-picked to manufacture a conclusion.

Explicit sleep, offline and network-change gaps are read from the bounded raw
window, including gaps superseded by later successful checks. If a candidate
sample starts at or before the latest relevant gap ends, comparison stops and
reports that gap's original measurement reference, time and reason. A recovered
link does not revive pre-change evidence from another target. Once every supporting
sample starts after the gap, comparison may resume. Missing records alone never
create a sleep, offline or network-change claim.

## Conservative explanations

The result includes selected evidence, the measurement references actually compared,
confidence, plain-language explanation, safe next step and limitations. Examples:

- Expected NXDOMAIN remains a matching DNS result; an unexpected NXDOMAIN stays a
  query-expectation difference, rather than an automatic resolver failure.
- DNS query problems with replies in another layer support reviewing the query and
  resolver policy, without proving that the resolver path worked.
- ICMP misses with DNS replies, including negative/error replies, keep protocol or
  target behavior plausible. The selected ICMP target's gateway role is unverified.
- An interface-down observation alongside responses produces mixed-link evidence,
  preserving possible timing/path differences instead of selecting a convenient
  winner. Interface down plus other recorded problems suggests inspecting that
  device's local connection first, without claiming a shared cause.
- External-target problems with other-layer replies remain scoped to the selected
  target/protocol. Failures in multiple layers remain observations, not an ISP,
  captive-portal, root-cause or internet-outage verdict.
- A partial ICMP/external sample cannot corroborate itself with its own replies.
  DNS error replies likewise cannot serve as their own other-layer response.

Confidence is unknown without a comparison and limited for comparable observations.
Matching checks describe only their selected expectations and original times.
No score, security finding, coverage state or internet-wide up/down verdict exists.
Any follow-up measurement remains separately authorized; recommendations never
rewrite routes, resolver settings or other network configuration.

Packet-free tests cover positive/negative DNS, refusal/error/truncated responses,
silent and partial checks, explicit gaps/recovery, temporal boundaries, family and
device separation, malformed input, ownership and order independence. These tests
validate interpretation only; they do not certify a collector, physical network,
packaged permissions, topology or whole-home coverage.

## Specialized external HTTPS input

The optional `HTTPS` snapshot and `HTTPSDeviceID` use the
[selected HTTPS evidence contract](external-https-evidence.md). They share the
same observing context and combined input limits. Generic HTTPS counts are
rejected in a comparison; HTTP error/redirect responses retain their status and
original expectation independently of whether the check matched. Explicit gaps
in this input also constrain comparisons. The retained adapter now supplies normalized HTTPS audits. Reading them does not
enable an external collector or grant new execution authority.
