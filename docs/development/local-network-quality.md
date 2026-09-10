# Local interface quality read

Related work: #14 and #29. This adds the first real OS-metadata producer for the
[network-quality assessment contract](network-quality.md). It is not a completed
connectivity diagnostic, background capability, or hardware-support claim.

## Entry point and authority

```text
cozysoc network-quality [--state-dir PATH]
```

The CLI uses the parameterless authenticated Unix-socket method
`network-quality.local`. The controller resolves the one active enrolled network
from storage. Callers cannot select a scope, interface, destination, URL, method,
command, or enablement flag. The existing peer-UID/session checks happen before
the handler, and unexpected parameters are rejected before metadata inspection.

No enrollment returns `enrolled: false` with no observer or check. It does not
inspect available interfaces to invent a default selection. Enrollment does not
need Device Watch enabled: this is a read of local OS metadata, like the existing
network setup reads, not authorization to collect neighbors or transmit traffic.
The [browser panel](browser-network-quality.md) now exposes this narrow read.
There is no new lifecycle action or API version bump.

## Evidence source and binding

The controller reuses the existing system interface inspector, which reads
`net.InterfaceByName` and that interface's address metadata. It does not read
neighbor caches, run a command, resolve DNS, or send a network probe.

Before attributing a result, it checks the stored interface name/index and the
complete canonical usable prefix set. Added or removed prefixes invalidate the
binding for this read, even if a common link-local prefix still matches. Multiple
addresses in one prefix do not count as a binding change. Loopback and
point-to-point interfaces are excluded. Link-local-only evidence cannot confirm
the binding. This stricter comparison does not change Device Watch preflight.

An administratively down interface can be reported only while the same binding
is still verifiable. Missing addresses, a changed binding, unavailable metadata,
or permission failure produce an unmeasured check, not an invented outage.
Interface/prefix matching cannot distinguish different networks reusing the same
binding. It is not a cryptographic network identity or authority for active checks.

The measured flag is **administrative up/down**, as defined by Go's
[`net.FlagUp`](https://go.dev/src/net/interface.go). It is not physical carrier,
Wi-Fi association, signal strength, gateway/DNS reachability, or internet access.
No latency, loss, throughput, signal score, or whole-home verdict is fabricated.

## Lifetime, bounds, and privacy

Each successful request performs at most one interface inspection. Up to 128
returned address prefixes are accepted for comparison; enrolled scope limits
remain unchanged. The existing UDS concurrency limit and request timeout apply.
Cancellation is checked before and after inspection; OS metadata calls themselves
are synchronous, not forcibly interrupted or wrapped in abandonable goroutines.

The result uses the assessment contract's validation and confidence semantics.
It includes the enrolled observer context, logical producer ID
`controller-local-interface`, source `os-interface-metadata`, sampling timestamps,
a transient evidence ID, and a thirty-second freshness bound. That freshness
bound is a presentation limit, not evidence of continuous collection. The logical
producer ID is not a newly enrolled sensor or stored sensor record.

Read results are not persisted, do not drive Device Watch coverage/verification,
and do not create audits, observations, or coverage samples. The transient
evidence ID is not a retrievable history record. Prefix values, host addresses,
MAC addresses, raw OS flags, and raw source errors are absent from the response.

## Validation and next slice

Synthetic tests cover administrative state, binding changes, duplicate-prefix
addresses, link-local-only limits, denied permissions, source failures, malformed
or oversized context, canceled reads, and invalid sample timing. UDS tests cover
authentication-before-handler, caller-input rejection, deadlines, and bounded
errors. Linux process E2E exercises the actual CLI/UDS/controller/OS path before
and after enrollment, confirms Device Watch stays off, and checks that reads do
not create monitoring history. The normal Darwin build remains compilation
coverage, not a macOS runtime, sleep/resume, carrier, or hardware certification.

The [shared frontend panel](browser-network-quality.md) now presents this read
with explicit source, freshness, and unknown-state copy. Separately authorized active gateway/resolver/external checks,
bounded durable history, corroborated diagnosis, and their privacy/routing/rate
controls remain future work. There is no automatic network configuration change.
