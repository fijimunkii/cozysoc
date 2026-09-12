# Selected-resolver review plan

Related work: #14 and #29. `internal/controller/resolverplan` creates immutable,
short-lived local review plans for the existing resolver wire/evidence contracts.
This is a pure planning boundary. No CLI, browser endpoint, sender, consent ticket,
route lookup, audit entry or scheduled check is added.

## Explicit selection

Each plan requires a controller-selected observer, enrolled prefix set and source
address, plus the exact numeric resolver endpoint, fully qualified ASCII query
name, A/AAAA question type, expected answer/NXDOMAIN/NODATA, immutable resolver and
query references, and an explicit destination policy. There are no defaults.

- `enrolled-prefix` requires the endpoint to belong to an enrolled prefix. This
  says nothing about the actual route, whether the endpoint is a resolver, or
  whether the query will remain local.
- `exact-endpoint` permits the one selected endpoint outside the enrolled prefixes.
  It does not authorize a subnet, substitute server, fallback or discovered
  endpoint. Membership is reported separately in the disclosure.

Both policies require a same-family source within the enrollment. The transport
family is independent of A/AAAA type. Prefixes must be canonical and bounded to
32, with no duplicates or ranges overlapping unspecified, loopback, mapped,
link-local, multicast or reserved high IPv4 space. Selected IPv4 network/broadcast
addresses are excluded in prefixes up to /30; /31 point-to-point and /32 host
prefixes are supported. IPv6 subnet-router anycast addresses are excluded except
for an explicitly enrolled /128 host. Every containing prefix can veto a selected
host, so broader overlaps cannot bypass a more-specific subnet exclusion.

The classic UDP profile allows port 53 only. Loopback, mapped/scoped/link-local,
unspecified, multicast and high reserved IPv4 endpoints remain unsupported.
Private, ULA and public unicast addresses are representable; representation is
not proof of a permitted route or production support for that family/platform.

## Required disclosure and budget

The explicit local disclosure includes the exact endpoint, canonical query name,
expected result, source/interface/enrollment, requested scope policy and whether
the endpoint is outside the enrolled prefixes. It always warns that the selected
recursive resolver may forward the name upstream, even for an enrolled address.
The client cannot bound traffic the resolver itself performs to answer a query.

The fixed proposed client ceilings are one send call, the exact encoded question
size, a 512-byte reply, at most 16 received datagrams and 512 receive calls, a
two-second exchange timeout within five seconds total, one concurrent run and a
one-minute controller-wide minimum run interval. Byte figures cover DNS payload,
not UDP/IP/link or neighbor-discovery overhead. Invalid/foreign datagrams consume
the receive budget. An uncertain send consumes the single call; it cannot license
a retry. There is no TCP/EDNS, referral, alias or host-resolver fallback.

A future executor must enforce these ceilings. Plan construction neither reserves
capacity nor proves enforcement. Consent must be for this exact one-shot plan,
not inferred from enrollment, enablement, the chosen policy or a previous check.

## Immutability and freshness

The plan owns canonical, sorted copies. Disclosure returns another copy and has
no conversion back to a valid plan. Generic formatting and JSON serialization of
the plan are redacted. The explicit disclosure contains private data and must
never enter general logs, telemetry or diagnostics.

Review lifetime is thirty seconds. Clock reversal and the exact expiry deadline
invalidate freshness. `SameSelection` compares all configuration, source,
observer, prefixes, disclosure flags and budgets, excluding review timestamps.
Changes are detected even if a caller mistakenly reuses old reference IDs. Owners
must still rotate those IDs when endpoint/name configuration changes to preserve
historical attribution. A matching fresh preflight does not extend old consent.

A future one-shot run controller must retain this immutable plan server-side,
consume an unguessable process-local ticket, independently revalidate enrollment
and route/socket binding, durably audit admission before sending, enforce the
original expiry/cancellation, and preserve partial or uncertain outcomes. Nothing
in this package can be used as that ticket or as send authority.

Synthetic tests cover scope escapes, configuration changes, overlapping prefixes,
IPv4/IPv6 host boundaries, canonicalization, private-copy ownership, redaction and
freshness. They do not establish live networking, VPN/split-DNS behavior, socket
binding, consent enforcement, durability or hardware support. #14 and #29 remain
open.

The [one-shot run controller](resolver-run-control.md) now consumes process-local
reviews and commits lifecycle/measurement audits through SQLite. The
[bounded UDP candidate](resolver-udp-sender.md) is exercised through it in the
isolated lab. Product execution endpoints remain pending.
