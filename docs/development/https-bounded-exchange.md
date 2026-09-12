# Bounded TLS/HTTP exchange

Related issues: #14 and #29. `httpsexchange.Exchange` implements the TLS and HTTP
portion of one [selected HTTPS review](https-review-plan.md). It accepts an
already connected stream from trusted native run control and owns/always closes
that stream. It does not dial or resolve a name. No product execution path invokes
it yet; saved settings and preview do not enable traffic.

## Required caller responsibilities

A future native caller must consume one-shot approval, reload enrollment/settings,
verify current route and actual TCP socket source/interface, and pass the original
absolute run deadline. The caller also owns connect-stage evidence, measurement
identity, durable audits, controller-wide concurrency and cooldown. This component
cannot establish these facts from a plan or a supplied connection. In particular,
its elapsed time starts after TCP connection; a caller must not refresh the total
run deadline when entering the exchange.

The component rejects missing deadlines and invalid/stale plans, then caps its
context to the earlier caller deadline, review expiry and eight-second exchange
ceiling. Cancellation closes the stream and joins the close callback. TLS uses a
three-second deadline; request write and response headers share a two-second
phase deadline, both capped by the existing total deadline.

## TLS, bytes and response handling

Production uses Go's TLS client with system roots, the exact configured DNS
identity, TLS 1.2–1.3 and HTTP/1.1 ALPN. It requires a verified certificate chain
and matching negotiated ALPN before writing HTTP. No client certificate, custom
verification bypass, session cache, resumption, proxy, name lookup or alternate
endpoint is provided. Test-only trust injection is unexported.

A stream wrapper bounds all TLS reads/writes, including handshake and buffered
response data, to 128 KiB read, 32 KiB written, 512 read calls and 64 write calls.
Reads are sliced to the exact remaining byte allowance; writes exceeding the
remaining allowance fail before touching the stream. Partial writes count only
accepted bytes and are never retried. These counters exclude TCP/IP/link overhead
and cannot limit bytes a peer transmits or the OS receives independently.

The client writes the exact reviewed HTTP request once. It distinguishes no HTTP
write, uncertain partial/error write, and complete local acceptance. Acceptance
is not proof of server receipt. It collects at most 16 KiB of CRLF-delimited
response headers across all informational and final responses, then uses Go's
standard HTTP parser. HTTP/1.0, upgrade responses, invalid final statuses and
malformed headers are rejected. Final statuses 200–599 are preserved, including
redirects and server errors; no redirect is followed and no response body is read.
TLS read-ahead may already contain body bytes and counts toward the transport
ceiling. The underlying connection is closed directly, without a further TLS
close-notify write outside the exchange budget.

Results contain stage, request acceptance, normalized outcome/status, byte/call
counts, and separate start/completion/final-header timestamps. The TCP candidate
retains the earlier connect start for end-to-end response timing. Raw TLS errors, certificates, headers, locations, request data and bodies
are excluded. TLS identity/protocol errors, stream failures, malformed HTTP,
timeouts, cancellation and budget exhaustion remain distinct. A response alone
is not an internet-availability or security claim.

## Evidence and remaining work

Owned loopback TCP fixtures exercise real TLS 1.2/1.3 handshakes, trust/name/ALPN
rejection before HTTP, exact request fields, informational responses, redirects,
error statuses, malformed/oversized headers and absent bodies. In-memory stream
tests exercise cancellation/deadlines and exact byte/call limits with partial
writes. These tests do not prove native socket binding, external reachability,
packaged permissions or one-shot consent. A [native TCP candidate](https-native-tcp.md) now verifies socket binding and
connects this exchange internally. Admission/audit integration and real isolated
end-to-end execution remain required before product
HTTPS traffic is exposed. #14 and #29 remain open.
