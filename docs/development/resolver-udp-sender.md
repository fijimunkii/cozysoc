# Bounded selected-resolver UDP sender

Related work: #14 and #29. `internal/controller/resolverudp.NewCandidate` creates
an inert sender implementing the resolver run controller's narrow executor. It
opens nothing until invoked by trusted code after audited one-shot admission.
No controller instance, browser/CLI execution endpoint, persistent configuration
or background scheduler is added. Other platforms explicitly return unsupported.

## One request and explicit transport binding

The macOS implementation revalidates the unscoped OS route before opening a
socket, again after setup before sending, and before accepting a result or
completed timeout. Every fresh selection must match the immutable admitted plan,
including exact endpoint/name, source, observer, enrollment and budgets. New route
evidence cannot renew the original consumed-approval context.

The socket is UDP only, nonblocking and bound to the selected numeric source and
interface. It stays unconnected; the sole send supplies the stored numeric
endpoint. There is no host resolver call, destination substitution, shell, raw IP
socket, route modification or privilege fallback. IPv6 sockets are v6-only.
Broadcast and address/port reuse are disabled. Bounded effective buffers and
required receive controls are read back. The sender verifies its exact bound
source, cryptographically selected high source port and interface before sending
and before interpreting a terminal result.

Four cryptographic random bytes provide a 16-bit transaction ID and a uniformly
selected port in 49152–65535. Binding collisions fail without retry. There is at
most one send syscall; even EINTR/EAGAIN or an uncertain error consumes it. The
packet must exactly encode the reviewed question. Kernel acceptance is evidence
of acceptance, not proof of wire egress or successful lookup.

## Deadlines and incoming evidence

The original absolute approval context is required. The sender additionally caps
it to five seconds total and the plan's expiry, checks wall and monotonic elapsed
time, and never extends its two-second response window. Receive work is bounded
by sixteen datagrams, 512 nonblocking receive calls and a 513-byte buffer that
recognizes over-512-byte replies. All foreign, malformed or truncated datagrams
consume the budget; exhaustion remains incomplete, not a timeout.

Incoming kernel peer, destination address and interface must all match. Darwin
IPv4 destination/interface controls and IPv6 packet-info controls are decoded
strictly, rejecting missing, duplicate, unknown or truncated controls. The
existing bounded DNS codec then matches ID/question and classifies the reply.
Wrong IDs, sources, questions and late replies are not success. A completed timeout
requires an accepted request and a full window followed by valid route/socket
rechecks. Route loss, cancellation, uncertainty and receive-budget exhaustion
remain distinct incomplete outcomes.

Matched timing includes the send call and is recorded at arrival, before final
route verification. Response duration uses the same wall-clock timestamp pair as
the stored interval; the monotonic clock separately bounds the wait. This avoids
mixing a finer monotonic counter with coarser wall timestamps (which can also
produce a legitimate measured zero). A later verification failure suppresses the
reply. Socket
cleanup failure can retain already validated evidence while failing the run.
The run controller preserves incomplete cleanup after review expiry without
allowing a completed reply/timeout outside the original approval window. No DNS
result alone declares an internet outage, a security finding or monitoring coverage.

Socket and route checks are snapshots, not an atomic guarantee that the network
cannot change between OS calls. Source/interface pinning constrains the actual
socket; no claim of comprehensive VPN, split-DNS or physical IPv6 support follows.
A sleeping process cannot keep observing, and wall/deadline checks invalidate work
after resumption rather than treating the missing interval as a completed timeout.

## Test evidence and remaining integration

Unit/race tests exercise matching, budgets, no-retry send failures, route/source
changes before and after send, cancellation, clock rollback, original deadlines,
cleanup errors and unsupported platforms. Fuzz tests exercise ancillary parsing.

The isolated macOS 15/26 lab runs the actual route inspector, resolver run
controller and UDP sender as the normal Terminal user. Its fixed test-only peer
answers only `test.example.` IN A on the owned feth pair, observes exactly one
query, and drops BPF setup privilege before processing. Required cases cover a
positive answer, NXDOMAIN, silence, a wrong ID, cancellation and source removal.
No public resolver or real household query is used. SQLite durability remains
independently tested; the live lab uses a validating in-memory auditor.

This is native direct-IPv4 lab evidence, not a production capability promotion.
IPv6 ancillary/policy behavior has synthetic tests only. Persisted immutable
configuration ownership, product consent/lifecycle integration, real routed and
IPv6 DNS validation, resolver history and truthful UI remain required. #14 and #29
stay open.
