# Gateway route/source preflight (metadata only)

Related to #14 and #29. The existing `cozysoc network-quality-plan TARGET_IPV4`
preview now includes a `route` review. It still cannot execute or grant consent.
The macOS adapter is a candidate implementation with synthetic and compilation
evidence; runtime validation on the reference Mac remains outstanding.

## What the native preview reports

`route.state` is `consistent`, `mismatch`, `unavailable`, or `unsupported`.
A consistent result includes source `darwin-rtm-get`, the route-associated local
IPv4 address, observation time, and a thirty-second evidence deadline. Errors
have fixed reason codes, no source address, and no invented observation time.
Linux/other platforms explicitly return `unsupported`; the original target and
budget review continues to work without a route implementation.

Consistency means the sampled unscoped routing-table entry points directly onto
the enrolled interface and its associated IPv4 address is assigned there, in a
subnet containing the target. It does **not** prove that a future socket uses that
source/interface, that the target is a gateway, or that any packet can reach it.
`send_binding_verified`, `execution_available`, `consent_granted`, and target
`role_verified` all remain false. No caller-supplied route/source/flags/budget is
accepted, and no browser or additional CLI/UDS route is introduced.

## Collection and policy

The adapter opens an IPv4-filtered **AF_ROUTE** socket, not an IPv4 data socket.
It sends exactly one local-kernel `RTM_GET` query for the already validated private
IPv4 target, requesting interface metadata. The request has no interface scope,
gateway, source address, routing changes, metrics updates, or resolve command.
It never forces `ifscope` to bypass an off-interface/VPN result. There is no DNS,
ICMP, UDP-connect trick, command execution, route-table dump, packet capture,
neighbor sweep, or network-configuration mutation.

The fixed little-endian Darwin `rt_msghdr` ABI is checked against Go's Darwin
constants. Replies must have matching PID/sequence, a successful completed GET,
valid lengths and four-byte sockaddr alignment, IPv4 destination/source, a valid
IFP name/index matching the header, and a contiguous route mask. Short compressed
netmasks are supported. Wrong types/correlations are ignored within a fixed read
budget. Malformed matching data never produces evidence. Link-layer address bytes
are structurally checked then discarded, not exposed or stored as neighbor data.

Only understood direct-route flags are accepted. Gateway hops, rejection,
blackholes, local/self routes, broadcast/multicast, redirects, proxy behavior,
external resolution, condemned routes, and unknown flag semantics fail closed.
The target cannot be a local address; the associated source must be an eligible
private IPv4 host in the same assigned subnet as the target. No source is guessed
from prefix membership or interface order.

Fresh local interface snapshots bracket the route read. Both must match enrolled
name/index and the complete canonical usable prefix set; the exact source address
must exist before and after. Added/removed prefixes, address loss inside an
unchanged prefix, down interfaces, and loopback/point-to-point interfaces prevent
a consistent review. Multiple assigned addresses in one prefix are allowed when
the actual route-associated address is present.

These reads are not an atomic network transaction. Changes between snapshots,
networks reusing the same binding, and application-specific Network Extension or
other socket policy are not excluded by a successful routing-table review.
A future sender must validate and enforce its own source/interface/route boundary
immediately before every send; this preview DTO must never become send authority.

## Bounds and evidence time

One inspection has a two-second context; the routing query has a one-second
context, fifty-millisecond socket I/O timeouts, at most sixteen received datagrams,
and a 4096-byte maximum accepted datagram. Interface comparisons accept at most
128 addresses. A private socket is closed on every return and protected against
inheritance by subprocesses. No abandoned goroutine is used to fake cancellation.
Synchronous kernel metadata calls themselves are not forcibly interruptible.

The route observation timestamp is captured immediately after its reply, not
refreshed by the later interface recheck or serialization. Its evidence deadline
and the original preview's review deadline remain distinct; neither is permission
to send. Backward/overlong clocks and caller cancellation discard the result.
No preview read enables monitoring, writes audits, or stores quality history.

## Validation and limits

Portable synthetic tests exercise binary parsing, request construction, masks,
PID/sequence correlation, malformed/truncated/oversized messages, kernel error
mapping, source identity, before/after binding changes, unsafe route flags,
cancellation, and clock boundaries. Fuzz seeds run in normal Go tests; fuzzing and
race tests were also exercised during this slice. Controller fixtures verify
projection/minimization and that consistency never grants authority. Linux
process E2E verifies explicit unsupported route metadata while previews still
leave monitoring off and history empty. CI compiles the new Darwin adapter tests.

No tests in this slice transmit probe packets. Darwin arm64/amd64 compilation is
not macOS runtime validation. Before connecting a sender, record owned-lab Mac
results for directly connected/private targets, self-targets, aliases, no-route,
VPN/routing changes, denied socket access, and interface/source loss. Also finish
one-shot consent/plan consumption, auditable outcomes, socket binding, bounded
reply matching, and controller-wide run-rate/concurrency enforcement.

Primary implementation references (API behavior, not copied implementation):
[Apple RTM_GET processing](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/rtsock.c),
[Darwin route message ABI](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/route.h),
and [Apple route GET request shape](https://github.com/apple-oss-distributions/network_cmds/blob/main/route.tproj/route.c).
