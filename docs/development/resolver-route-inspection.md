# Native resolver route inspection

Related work: #14 and #29. `internal/controller/resolverroute` inspects one
explicitly configured numeric resolver destination on macOS and builds a fresh,
source-bound resolver review plan. It reads interface metadata and sends one
unscoped `RTM_GET` over an `AF_ROUTE` socket. It sends no IP/DNS packets, changes no
routes and does not choose a resolver through host DNS. Other platforms return
unsupported. No product endpoint or controller-owned instance is added.

## Enrollment, source and next hop

The controller must supply its current authorized enrollment and immutable
resolver configuration. The observer and selected interface must match by name
and index, be up, and be neither loopback nor point-to-point. The complete usable
prefix set must match enrollment before and after lookup. Link-local interface
addresses are excluded from enrollment comparison but can establish membership
for a scoped IPv6 next hop.

The unscoped OS route must cover the exact target and select the enrolled
interface. The route-associated source must still be an exact assigned address
before and after lookup. The inspector does not guess among interface addresses.
A source change, local/self destination, altered enrollment, interface switch or
clock reversal leaves the check unavailable. The resolver plan then applies all
of its endpoint, source-family, subnet-host and destination-policy restrictions.

Direct routes require a matching link interface and target membership in the
source address's actual interface prefix. Routes through a gateway require a
same-family unicast next hop belonging to an actual prefix of the selected
interface. IPv6 link-local next hops require matching scope IDs. The route's
source and target still obey the plan's global/private-unicast profile; link-local
resolver endpoints and link-local sources remain unsupported. A valid route
through a next hop does not expand the reviewed exact-endpoint scope.

Reject, blackhole, local, broadcast, multicast, redirect/dynamic and unknown route
flags are rejected. No interface-scoped request overrides a VPN or OS route.
Physical interfaces selected through a different route remain mismatches. This
is not comprehensive VPN or split-DNS support.

## Bounded metadata decoding

The maintained `golang.org/x/net/route` dependency already present in the project
encodes requests and decodes typed IPv4, IPv6 and link addresses. A Darwin
validation wrapper requires a complete datagram of at most 4096 bytes, matching
PID/sequence, understood address slots, exact sockaddr boundaries, contiguous
netmasks, matching interface indices and canonical destination prefixes.

Darwin errno is read from the authoritative `rt_msghdr` offset 24. The dependency's
current general decoder reads offset 28, which is Darwin's use counter; that
value is not used as an error. Radix netmask family/port/flow positions can contain
mask bits, so the wrapper pins mask decoding to the already validated destination
family. KAME embedded and explicit IPv6 gateway scope IDs cannot contradict one
another. Parser panics and OS errors are converted to fixed, non-private errors.

A lookup has a one-second context, at most sixteen received route datagrams and
128 receive calls, with bounded socket timeouts. Full inspection has a two-second
ceiling and checks both elapsed monotonic and wall time before returning evidence.
No route/neighbor table dump is retained. The route observation time is recorded
immediately after lookup; subsequent interface checks and plan construction do
not refresh it. This ordering matches resolver run-control revalidation.

## Evidence and remaining work

Synthetic tests cover direct/routed IPv4 and IPv6, compact masks, errno/use-counter
separation, foreign messages, malformed lengths/flags, source changes, enrollment
races, scope IDs, cancellation and time limits. Darwin fuzzing exercises the
bounded decoder. The isolated macOS 15/26 lab additionally checks the real native
IPv4 resolver route and wrong-interface rejection on its owned virtual network.
It sends no DNS query and does not require a DNS server.

A successful metadata lookup is not proof of the actual source of a UDP socket,
resolver reachability, DNS correctness or consent. In particular, a route's IFA
may differ from the source an unbound socket would choose; unsupported or
link-local route-associated sources are not replaced with guesses. Native IPv6
and routed-resolver paths currently have synthetic decoder/policy evidence only,
not a physical-network support claim.

The bounded DNS sender must independently verify actual socket source/interface,
revalidate the route before sending, and honor the original consumed approval and
all traffic/deadline bounds. Immutable persisted configuration, product lifecycle
and consent integration, real DNS lab evidence and history UI remain required.
#14 and #29 stay open.

The [bounded UDP candidate](resolver-udp-sender.md) now revalidates these selections
and independently verifies the actual socket. It remains disconnected from
product execution endpoints.
