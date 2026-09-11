# Gateway check preview (native, review only)

Related to #14 and #29. This prepares a narrow active-check review boundary after
the local metadata read and browser panel. It does **not** implement a probe,
record execution consent, or complete connectivity diagnosis.

## Entry point

```text
cozysoc network-quality-plan [--state-dir PATH] TARGET_IPV4
```

Select a numeric private IPv4 address believed to be the gateway of the already
enrolled network. The output explicitly says `preview-only`,
`execution_available: false`, `consent_granted: false`, and
`role_verified: false`. It contains the selected address, enrolled scope and
interface/prefix binding, creation/review-expiry times, ICMP method, proposed
traffic limits, and privacy/interpretation limitations. Review expires after
thirty seconds; the preview is neither a capability token nor a signed approval.
Reissuing a preview never accumulates consent or schedules work.

There is no default address, automatic gateway guess, browser route, execution
command, or approve/run API. A [macOS route/source preflight](gateway-route-preflight.md)
now inspects local routing metadata for the selected target; other platforms
explicitly report unsupported route inspection. Existing Overview reads are
unchanged. The destination is user-selected; prefix membership does not verify
that it is a gateway, reachable, or even another machine rather than this host.

## Scope and authority

The authenticated UDS method `network-quality.gateway-plan` accepts only
`{"target":"..."}`. Unknown fields fail before the handler. Scope, interface,
budgets, method, and consent cannot be supplied by the caller. Existing peer,
session-secret, version, request-size, concurrency, and timeout checks apply.

The controller resolves the one enrolled scope, validates the target and stored
context, and then reads that interface's OS metadata once. It shares the local
quality read's **complete-prefix-set** comparison, not discovery's weaker
any-matching-prefix check. The binding must still match and be administratively
up. Missing enrollment, changed/down/unsupported bindings, source failures,
cancellation, invalid clocks, and overlong metadata reads return bounded errors,
not a plan suggesting execution is available.

The first preview profile admits only canonical RFC1918 IPv4 hosts contained in
an enrolled IPv4 prefix wholly within RFC1918 space. Hostnames, URLs, ports,
CIDRs, IPv6/mapped IPv6, link-local, CGNAT, public/external addresses, and /31-/32
target prefixes are unsupported. Network/broadcast endpoints of **any** matching
prefix are rejected, including overlaps. Unrelated IPv6 enrollment prefixes
remain in the full binding; they do not authorize IPv6 probes. The pure builder
bounds and copies its input and canonicalizes prefix order without mutation.

Matching metadata cannot identify networks that reuse the same prefixes and
interface. The new route review can establish sampled routing-table/interface
address consistency, not a future socket's source or egress binding, gateway role,
ICMP permission, or application-specific routing policy. No sender exists.

## Proposed executor limits, not implemented enforcement

The fixed profile proposes three ICMP echo attempts, at least one second between
attempt starts, a one-second attempt deadline, and a five-second total deadline.
It proposes one concurrent run and a minimum sixty-second controller-wide run
interval (not per target), with no background scheduling or hidden retries.
There is no configurable caller override in this initial profile.

Each attempt proposes 32 payload bytes. The maximum 120 request bytes counts
three eight-byte ICMP echo headers plus data. This is **not total wire traffic**:
IP/link headers, neighbor resolution, link retransmissions, and replies are
excluded. A future executor must separately bound reception and disclose those
limits. Probes can be logged by the destination or intervening infrastructure;
no DNS lookup, third-party endpoint, or household payload is planned.

These ceilings are reviewable data and future implementation requirements.
There is no current traffic-limiting implementation to certify. Execution must
add explicit one-shot consent, authoritative plan storage/consumption, expiry,
revalidation before every send, verified source/route/interface binding,
permissions, cancellation, bounded reception, reply matching, controller-wide
rate/concurrency enforcement, and audited completion/failure. Never accept this
client-visible DTO as authority to send. An ICMP failure alone must not become an
internet-down verdict or a security finding.

## Evidence

Pure policy tests cover destination syntax/scope, overlapping subnets, context
bounds, fixed ceilings, clock bounds, and input isolation. Controller tests cover
enrollment, complete binding, cancellation, safe errors, and no implicit grant.
UDS tests cover authentication, narrow inputs, error mapping, absent execution
methods, and bounded handler contexts. Linux process E2E previews only local
metadata before/after enrollment and checks that Device Watch remains off and
that previews create neither audits nor observation/coverage history. Its
enrolled portion needs an RFC1918 interface and otherwise explicitly skips.

No tests send ICMP or otherwise probe public/unowned networks. Cross-compilation
is not macOS runtime, route-binding, ICMP-permission, or hardware validation.

References: [Go numeric address parsing](https://pkg.go.dev/net/netip),
[private IPv4 addressing](https://www.rfc-editor.org/rfc/rfc1918), and
[ICMP echo format](https://www.rfc-editor.org/rfc/rfc792).
