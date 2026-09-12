# Explicit selected-HTTPS review plans

Related issues: #14 and #29. `httpsplan.New` now builds an immutable local review
of one explicitly selected HTTPS request. It extends the
[normalized HTTPS evidence contract](external-https-evidence.md) with private
configuration, exact request encoding, destination disclosure and required
executor limits. It performs no I/O, chooses no external service, stores no
configuration and grants no consent. No external collector or CLI/browser
execution is enabled by this work.

## Exact configuration and binding

The caller supplies immutable selection/endpoint/request references, one numeric
IPv4 or IPv6 endpoint on port 443, an explicit ASCII DNS name for TLS verification
and HTTP Host, GET or HEAD, an origin-form request target, an expected final status,
and the `exact-endpoint` policy. There is no hostname-to-address resolution,
automatically selected resolver, alternate address or family fallback. Endpoint
and TLS name are disclosed independently. TLS IP identities, trailing-dot names,
Unicode/IDNA conversion, custom ports, user information and arbitrary headers are
outside this first profile. DNS-name case is normalized to lowercase.

The request target includes its exact path and optional query, bounded to 1024
bytes. Absolute/network-path URLs, fragments, raw whitespace/non-ASCII bytes,
backslashes, invalid escapes and encoded control characters are rejected before
encoding. Valid escaped bytes and query spelling are preserved by Go's standard
HTTP request writer. Only the disclosed Host, fixed user agent, `Connection:
close`, `Accept: */*`, `Accept-Encoding: identity` and `Cache-Control: no-store`
headers are emitted. There are no cookies, authorization headers, request body
or device identifiers. Request contents can themselves be private and belong
only in protected configuration and deliberate disclosure.

The controller must supply the enrolled observer, canonical prefix list and
selected source address. `checkbinding` shares the existing resolver plan's
source/target rules: eligible same-family unicast hosts, source membership,
32-prefix bound, duplicate/default/broad-excluded-range rejection, and overlapping
subnet vetoes for network/broadcast/anycast addresses. IPv4 /31 and exact host
routes remain supported. Private and documentation address space are accepted
for explicit settings and isolated labs; this is not a public-internet classifier.
Target membership is disclosed, but neither membership nor lack of membership
proves gateway identity, an external route, source assignment or actual egress.
Fresh OS route/interface inspection remains required before execution.

## Immutable review and disclosure

`Plan` has no writable fields or decoder. `Configuration` and `Plan` are redacted
under ordinary formatting and JSON serialization. Explicit `Disclosure()` returns
an owned copy containing the private endpoint, TLS name, request and binding,
plus policy, budget and privacy text. That copy must not enter generic diagnostics,
normalized observations, telemetry or browser APIs. It is not an executable plan.
`RequestBytes()` returns an owned buffer with the exact reviewed request; possessing
those bytes does not authorize sending them.

A review expires after 30 seconds, is invalid at exact expiry and on clock reversal,
and uses bounded signed-nanosecond times. `SameSelection` compares all configuration,
binding, policy and budget values while ignoring review times and prefix ordering.
A new review cannot renew an earlier approval. Configuration references must rotate
when settings change; durable allocation/immutability is still future storage work.

## Required executor policy and limits

The fixed profile requires HTTP/1.1 with only the `http/1.1` ALPN, TLS 1.2–1.3,
system trust and selected-name verification. It requires a fresh connection with
no client authentication, session resumption or early data. Name lookups, proxies,
redirects and response-body reads are disabled. These values are requirements for
a future executor, not evidence that any runtime behavior has been enforced.

Each plan discloses:

- One connection and one request, with zero retries.
- Exact encoded request size and a 16 KiB total response-header ceiling, including
  informational responses.
- At most 128 KiB read and 32 KiB written through the TLS-bearing TCP stream,
  including handshake and buffered response data; at most 512 reads and 64 writes.
- Two seconds for connection, three for TLS, two for response headers and eight
  seconds total, including the whole attempt rather than resetting per phase.
- One concurrent run and a one-minute controller-wide run interval.

TCP retransmissions and IP/link overhead are outside these stream-byte counts.
A server can send body bytes even when the collector stops at the headers. The
limits therefore are not a precise network-data or billing estimate. The review
explicitly discloses the operator's visibility of the connecting address, timing,
TLS identity and request; possible network visibility of the TLS name; and the
fixed `CozySOC-Network-Check/1` user agent. Encrypted ClientHello is not in this
profile. Metered connections and VPN/binding changes need deliberate review.

Tests verify exact standard-library request encoding, escaping and bounds,
redaction/explicit disclosure, ownership, stale/reversed clocks, changed selections,
IPv4/IPv6 membership and hostile inputs. Existing resolver plan/route/coordinator
tests exercise the extracted shared validation. No test here demonstrates actual
TLS, public-endpoint, packaged permission or hardware behavior. Before traffic is
exposed, remaining work includes durable settings, native route/socket binding,
actual byte/deadline enforcement, one-shot consent and durable audit/recovery.
