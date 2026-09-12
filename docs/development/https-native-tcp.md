# Native HTTPS TCP candidate

Related issues: #14 and #29. `httpstcp.NewCandidate` constructs an inert native
candidate. `Execute` combines a pinned TCP connection with the existing
[bounded TLS/HTTP exchange](https-bounded-exchange.md). No product endpoint,
scheduler or consent flow invokes it yet.

## Admission and route lifetime

The caller must supply the original absolute run deadline and a current immutable
selection from controller-owned preflight. The candidate rejects stale or extended
route evidence, revalidates the route before connection, and caps the entire
operation to eight seconds or the earlier caller/review deadline. Connect has a
two-second ceiling inside that original deadline. Fresh inspections never refresh
the reviewed selection or run deadline. A per-instance guard rejects concurrent
execution; durable cooldown and audited one-shot admission remain future run-control
responsibilities. Possessing a plan or invoking this internal API is not consent.

The route inspector must reproduce the exact selection, interface, source and
prefixes. Rechecks occur before connection, after connection, before every TLS
stream write, and after a successful response. A changed route, source or scope
prevents further writes and removes response attribution. This does not promise
an atomic kernel route lock: ordinary network changes can occur between checks.

## Darwin socket binding

The production connector uses Go's TCP dialer with one canonical numeric endpoint,
an explicit IPv4/IPv6 family, the selected local source, and keepalive disabled.
There is no hostname resolution, proxy, alternate address, application retry or
TCP fast-open path. OS TCP SYN/data retransmissions remain outside application
retry and TLS-stream byte ceilings.

Before connect, a native control callback sets and reads back the bound-interface
option, disabled keepalive/reuse/don't-route options, stream socket type and IPv6
only mode when applicable. After connection and before every write, the connector
reads those options plus the actual socket source and peer. The source must match
the reviewed address with a nonzero ephemeral port; the peer must equal the exact
configured endpoint. Unavailable options, changed bindings or canceled contexts
fail closed. Other operating systems return unsupported without dialing.

Every path closes an opened connection. TLS/HTTP byte, call, header and phase
limits remain enforced by the exchange. The candidate retains connect-stage
failure separately from TLS/request stages, normalizes errors, and clears status
attribution if binding changes during the exchange. It does not yet allocate
measurement identities or write durable run evidence.

## Evidence and remaining integration

Candidate fixtures exercise exact target/deadline propagation, one connect,
per-write route checks, changed bindings at each phase, status invalidation,
cancellation, stale selections and concurrent admission rejection. Darwin tests
exercise real IPv4 and IPv6 TCP binding/readback using owned loopback sockets and
reject changed source, peer and native options before another write. That private
socket primitive is used with loopback only in tests; production entry points
retain plan and route validation that excludes loopback destinations/interfaces.

These tests establish socket-option behavior, not physical NIC, routed external,
VPN, packaged-permission or full HTTPS run support. TLS exchange has separate real
loopback evidence; route inspection has separate isolated native lab evidence.
An integrated native HTTPS lab, measurement/audit coordinator, durable cooldown
and interactive one-shot consent remain required before enabling product traffic.
#14 and #29 remain open.
