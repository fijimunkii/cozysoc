# Bounded gateway ICMP candidate (experimental opt-in only)

Related to #14 and #29. This adds `internal/controller/gatewayicmp` after the
[one-shot run coordinator](gateway-run-control.md). It supplies a small candidate
macOS ICMPv4 transport and packet-free unit tests. It remains off by default.
A separate [native macOS lab](macos-network-lab.md) exercises real isolated packets.
The [native consent session](gateway-consent-session.md) can reach it only with
explicit experimental macOS controller opt-in and connection-bound approval. No
browser control, scheduler, privilege escalation or new dependency is installed.
The [interactive command](interactive-gateway-check.md) requires an explicit
terminal decision through this same gated native path. Existing gateway
previews still report execution unavailable and consent not granted.

## Authority and integration boundary

`NewCandidate` opens nothing. `Measure` is for trusted controller code after
explicit, audited one-shot admission; a `Request` is not a wire schema and must
never be constructed from an untrusted preview DTO. The caller must retain the
original consumed review deadline in its context even when preflight is newer.
The sender independently validates the selected private IPv4 target/source,
complete enrolled binding, evidence deadline, and exact fixed profile.

The [gatewayrun adapter](gateway-run-measurements.md) now carries samples through
one-shot admission, validation and a versioned terminal audit. The
[controller lifecycle](gateway-controller-lifecycle.md) owns it dormant, with no
production run entrypoint. Execution completion remains separate from measurement.
The controller-wide one-minute cooldown, one-shot consent and single coordinator
ownership remain `gatewayrun` responsibilities. A sender instance rejects a
concurrent sample but is not a cross-instance, process-wide or persistent limiter.

## Data socket and per-send checks

On Darwin only, the candidate opens an `AF_INET / SOCK_DGRAM / IPPROTO_ICMP`
socket. There is no raw-IP fallback, shell command, DNS lookup, arbitrary payload,
port selection, destination override, or automatic privilege escalation. Other
platforms return unsupported before creating a data socket.

The candidate first requires fresh unscoped route/interface/source consistency
from the existing route inspector. It then binds the exact reviewed local IPv4
address and `IP_BOUND_IF`, disables broadcast and header inclusion, keeps normal
routing rather than forcing `SO_DONTROUTE`, and uses TTL 1. The socket is private,
nonblocking and close-on-exec. It remains unconnected; every send uses its one
stored numeric destination.

After pacing and immediately before each send, the sender collects fresh route
metadata and checks that source/name/index still match. The transport reads back
its own bound local address, interface, socket type, required options, and bounded
effective socket buffers. Failure stops the sample with no fallback or retry.
A successful source/interface option readback is not hardware proof of actual
wire egress. The route and socket checks are not atomic with network changes;
Network Extension policy, same-binding network reuse, and queued kernel/link
behavior require owned-lab validation. TTL 1 is an additional limit, not proof
that a target is the gateway or that all traffic stays inside an authorized LAN.

## Traffic and receive limits

The fixed profile permits at most three forty-byte ICMP echo requests: an
eight-byte header and thirty-two random payload bytes. One random identifier and
nonce plus per-attempt sequence numbers correlate replies; no household payload,
process ID or credential is placed in the echo. Correlation is not authentication
of the destination, especially against an observer who can see the request.

There is at least one second between attempt starts, implemented conservatively
from the preceding send syscall's completion. A slow operation never causes a
catch-up burst. Each attempt has at most a one-second reply window and the entire
sample at most five seconds, further shortened by the plan and caller deadlines.
Wall rollback fails closed; the exact expiry boundary is already expired.
The absolute wall deadline carried by the caller context is checked independently
of its cancellation timer, including immediately before the Darwin send syscall,
so system suspend cannot extend an older approval through newer plan evidence.
No send error, including interruption or would-block, is retried because send
outcome can be uncertain. Cancellation stops new work without detaching a worker.

Reception uses nonblocking `recvmsg`, paced ten-millisecond waits when no message
is available, and a hard ceiling of 512 receive calls and sixteen received
datagrams across the **whole sample**, not per attempt. Each call has a 512-byte
data buffer and 256-byte ancillary buffer. Truncated, malformed, duplicated,
stale-sequence and foreign datagrams consume the receive budget. Effective socket
send/receive buffers must each be no more than 64 KiB. These are application and
socket-buffer limits, not a claim to bound all inbound link traffic or kernel
memory accounting under a flood.

The 120-byte request ceiling counts ICMP headers and data only. IP/link headers,
neighbor resolution, replies, and link retransmissions are not included. A kernel
accepted send does not prove on-wire transmission, and a syscall error must not
be treated as proof that nothing was sent.

## Matching and measurement semantics

`IP_STRIPHDR` makes the receive format explicit. Only an exact echo reply with a
valid checksum, code, identifier, sequence and nonce is accepted. Its peer must
be the selected target; `IP_RECVDSTADDR` and `IP_RECVIF` ancillary evidence must
match the bound source and enrolled interface. Missing/duplicate/unknown controls,
invalid native lengths, and truncated control messages cannot produce a reply.
Link-layer bytes in interface metadata are discarded rather than retained.

A `Sample` includes observing scope/interface, numeric target/source, times,
send-call count, kernel-accepted request count, matched replies and completed
timeout windows. `Complete` is true only after all three attempts finish normally
and cleanup succeeds. An error retains partial counts but leaves `Complete` false
and mean RTT absent. Incomplete work is not 100% loss; unsent attempts are not
counted as unanswered. No packet-loss percentage or internet/security verdict is
produced by this package.

For a complete sample, zero replies leaves RTT unknown. A measured zero duration
is distinct from unknown. RTT is host-observed time from immediately before the
send path to matched reply processing, including socket verification, syscall
and scheduling overhead; it is not a hardware timestamp or isolated link latency.
A timeout means no matching reply was processed within that attempt's window,
not proven gateway failure. Samples contain no packet bytes, nonce, raw errors,
credentials, execution tickets or fabricated throughput values.

## Evidence and remaining gates

In-package tests are synthetic and packet-free. They cover the exact request
profile, per-send route/source/socket checks, no-burst pacing, deadline and
cancellation boundaries, partial results, errors without resend, timeout/unknown
latency, receive exhaustion across attempts, stale/foreign/duplicate replies,
malformed checksums/ancillary metadata, clock changes, entropy failure, input
isolation, concurrent-use rejection, and cleanup. Portable fuzz tests cover
packet/control parsing. In-package Darwin-only tests check ABI/error mappings and
canceled/invalid inputs without opening sockets. CI also retains Darwin compilation.

The separate [native macOS isolated-network lab](macos-network-lab.md) now runs
the actual route inspector and sender against a bounded virtual Ethernet peer.
It checks real replies, timeouts, mismatched replies, cancellation and source
loss. Passing evidence is explicitly scoped to the runner version and normal-user
Terminal launch context; it is not proof of a packaged app's permissions or of
physical egress. The sender requires no test-only verification bypass.

Before broad user-facing exposure, validate the packaged execution context and remaining
owned-lab scenarios: VPN/route changes, interface recycling, denied-permission
recovery, receive overload, physical sleep/resume and observed hardware egress.
The experimental native path now exercises authenticated consent and durable
results through the real controller and interactive terminal command. Do not promote a virtual
fixture to hardware certification or silently start probing after enrollment.

Primary references (protocol/API behavior, not copied implementations):
[RFC 792 echo format](https://www.rfc-editor.org/rfc/rfc792),
[Apple datagram ICMP options and send path](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/netinet/ip_icmp.c),
[Apple receive header/control handling](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/netinet/raw_ip.c),
and [Apple destination/interface control messages](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/netinet/ip_input.c).
