# Selected external HTTPS evidence

Related issues: #14 and #29. `networkquality.AssessHTTPS` validates and interprets
bounded normalized evidence from one explicitly selected endpoint/request at a
known observing interface. `Corroborate` can consume this specialized snapshot
alongside ICMP, local-link and DNS evidence. This is a pure evidence contract:
there is no external collector, CLI/browser execution,
DNS resolution, TLS connection, HTTP request, scheduling or new consent path.
The existing retained diagnosis continues to compare its gateway/DNS pair only.

## One selected exchange

`HTTPSSelection` contains immutable endpoint and request references, IPv4 or IPv6
transport, GET or HEAD, and one explicit expected final HTTP status (200–599).
No service, URL or expected status is chosen by default. An expected error or
redirect can match a deliberately selected test; a match is limited to the status.
Endpoint references must change when pinned address/TLS identity changes; request
references must change when method, path or expectation changes. No raw address,
name, path, headers, cookies, credentials, redirect location or response body
belongs in this evidence. References are attribution, never execution authority.

The measurement distinguishes connection, TLS and HTTP request stages. Reaching
request stage requires the collector to have verified the selected TLS identity.
Connection/TLS failures cannot carry a request or HTTP response. A transport
error after connecting stays separate from a TLS verification/protocol failure
and preserves whether HTTP bytes were accepted or uncertain. Request state
records not-sent, accepted or uncertain HTTP bytes; not-sent does **not** mean no
TCP/TLS packets were sent. A timeout records its stage, while cancellation or an
unfinished exchange remains unknown. No uncertain work is retried by this code.

Only a bounded, parsed final response header from the selected verified exchange
can carry a status. Informational responses are not final results. Statuses
301/302/303/307/308 remain redirect responses; 304 is a status response, not an
instruction to contact another destination. HTTP errors and redirects are still
responses, independently of matching the original expectation. The assessor
never follows a redirect, validates a body, measures packet loss, diagnoses a
captive portal, or turns TLS verification into a security posture claim.
Optional response timing includes connection/TLS time through final header receipt.

These distinctions follow [HTTP semantics, RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html).
Captive-portal state requires separate evidence; a redirect or TLS failure alone
is not the [Captive Portal API defined by RFC 8908](https://www.rfc-editor.org/rfc/rfc8908.html).

## Bounds and comparison

The existing limits apply: 16 selections, 128 measurements, a 24-hour evidence
window, at most 30 seconds per measured exchange, and explicit freshness from one
second to fifteen minutes. These are validation ceilings, not proof of collector
resource enforcement. Times must fit signed nanoseconds. Duplicate identifiers,
tied completion times, changed selection context and inconsistent stage/status/
request/gap claims fail the entire input without partial success.

The latest sample is selected before freshness evaluation. New gaps and unfinished
work supersede earlier successes. At exact expiry evidence is historical; original
status, expectation match, stage, request state and timing remain in an owned
copy, while the fresh response metric disappears. Missing evidence stays unknown.

The optional HTTPS comparison input must identify the same observing device,
scope, interface, assessment time, window and freshness as the other snapshots.
Sensor references remain distinct. Combined selection/measurement bounds cover all
three inputs; duplicate measurement IDs are rejected across them. IPv4 and IPv6
are compared separately. Recorded sleep/offline/network-change gaps in any input,
including superseded or other-family gaps, still constrain the comparison.

Generic HTTPS success/failure counts are rejected by `Corroborate`: they lose the
difference between response receipt and expected status. HTTP errors can therefore
corroborate replies alongside ICMP misses, without being called packet loss or
external silence. One external layer cannot corroborate itself, and no comparison
establishes an internet outage, common root cause, security or monitoring coverage.
The earlier standalone generic `Assess` fixture contract remains available.

## Remaining collection work

The [explicit HTTPS review plan](https-review-plan.md) now specifies pinned
configuration, exact request encoding, disclosure and required executor ceilings.
It remains pure. [Durable HTTPS settings](https-configuration.md) now preserve
explicit configuration separately and expose [native save/list/retire commands](https-controller.md).
These paths do not enable a collector.

Before external traffic is exposed, a collector must implement explicit immutable
destination configuration and local disclosure of the exact endpoint, TLS identity,
request, data use and privacy impact. It must enforce eligible pinned addressing,
route/source/interface binding, certificate verification, no implicit proxy or
DNS fallback, no redirects/retries/alternate destinations, bounded bytes/requests/
deadlines/concurrency/cooldown, separate one-shot consent and durable audits.
An external operator can observe the connecting address, timing and requested
resource; that disclosure cannot be replaced by an opaque reference.

Fixture tests cover expected and unexpected HTTP statuses, redirects, connection
and TLS failures, stage-specific timeouts, uncertain/canceled work, missing and
stale evidence, explicit runtime gaps, family/context separation, contradictory
claims and cross-layer response semantics. They make no real-network, TLS runtime,
permission, packaged-platform or hardware support claim.
