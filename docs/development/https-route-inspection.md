# Native HTTPS route inspection

Related issues: #14 and #29. `httpsroute.NewInspector` verifies the current local
route and source for an explicitly configured numeric HTTPS endpoint on macOS.
It builds the existing [HTTPS review plan](https-review-plan.md) from verified
metadata for the [native preview command](https-controller.md) and the gated
one-shot [HTTPS run](https-consent.md). Inspection alone grants no approval or
send authority.

## Shared metadata checks

`checkroute` now owns the metadata-only inspector and Darwin message parser
previously in `resolverroute`. Both HTTPS and resolver adapters validate their
own configuration, inspect the exact numeric destination, and build their own
protocol-specific plan. No DNS configuration or dummy DNS query stands in for an
HTTPS target. Existing resolver route, sender and coordinator behavior continues
through the same shared metadata checks.

The inspector reads the selected interface before and after one unscoped
`AF_ROUTE` lookup. Interface name/index, usable prefixes and exact assigned source
must match enrollment. Down, loopback and point-to-point interfaces fail closed.
The returned route must cover the destination and select the enrolled interface;
no interface-scoped lookup overrides the OS route or VPN. Direct routes require
on-link membership; routed destinations require an eligible, on-link next hop.
Scoped IPv6 link-local next hops are allowed, while link-local source and target
addresses remain unsupported. See the [route validation details](resolver-route-inspection.md)
for parser bounds, rejected flags, next-hop checks and source limitations.

One lookup remains bounded to one second, sixteen received route messages and
128 receive calls; the complete inspection is bounded to two seconds. It sends
no IP packets, changes no routes and retains no routing-table dump. Other
platforms return unsupported with no fallback.

The result retains `RouteObservedAt` and `RouteFreshUntil` independently of plan
creation. Follow-up interface reads and review construction never extend the
thirty-second route lifetime. Cancellation, clock reversal, stale inspection,
missing metadata, source loss or enrollment mismatch returns no selection.

## Evidence and support limits

Shared fixture and Darwin parser tests cover direct/routed IPv4 and IPv6,
malformed route messages, scope IDs, source/interface changes, cancellation and
clock bounds. Adapter tests verify protocol configuration rejection before
metadata reads, failure propagation, owned review prefixes and preserved route
age. The required macOS 15/26 lab case inspects the real route to its isolated
IPv4 peer using an HTTPS configuration and rejects a changed interface index.
It needs no TLS server and performs no TLS/HTTP exchange. Native routed and IPv6
paths retain synthetic evidence only.

Route inspection does not prove a TCP socket used that source/interface, that a
TLS identity is valid, or that an endpoint is reachable. Controller-owned
preflight reloads enrollment and immutable settings; the
[native TCP candidate](https-native-tcp.md) independently binds and verifies its
socket while enforcing the disclosed traffic limits. The
[experimental consent path](https-consent.md) adds one-shot approval and durable
audits. These integration checks do not certify physical networking, packaged
permissions or general support; #14 and #29 remain open.
