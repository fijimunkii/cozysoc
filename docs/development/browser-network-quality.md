# Local connection in the shared frontend

Related to #13, #14, and #29. This exposes the existing
[local-interface read](local-network-quality.md); it does not add active probes,
a background quality capability, or complete connectivity diagnosis.

## User experience

Overview contains a separate **Network quality / Local connection** panel.
It starts with no sample and reads only when the user chooses **Read local
interface**. **Refresh local sample** and **Retry local sample** affect this panel
alone. There is no background polling and no automatic read on focus or resume.
The primary navigation stays Overview, Devices, Activity, Coverage, and Tools.

The result identifies this controller and the enrolled interface name/index,
operating-system metadata source, completion time, freshness deadline, and
limited/unknown confidence. Administrative Up/Down is explicitly a sampled OS
state, not physical carrier, Wi-Fi association, gateway/DNS/internet reachability,
a security verdict, or monitoring coverage. Unsupported, permission, source, and
binding gaps have separate curated explanations; missing metrics remain unknown.
No enrollment shows a setup explanation without selecting an interface or
enabling Device Watch. No new mutation is exposed.

A failed quality read does not discard the core live bundle or replace it with
demo data. The synthetic demo shows an explanation, no local measurement and no
read/refresh control. Navigating away, switching modes, or refreshing setup
unmounts the live panel and aborts/invalidates in-flight responses.

## Freshness and request lifetime

Samples retain the native thirty-second evidence deadline. The browser caps this
with elapsed request time and both wall and monotonic clocks: response arrival
never grants an old sample a fresh thirty seconds. Clock rollback or future-dated
evidence fails closed for current presentation. Expiry is half-open; at the exact
deadline the sample is historical. Historical administrative values are labeled
**Last sampled**, not presented as the current connection state.

A bounded local timer ages the display without taking another sample. Window
focus/blur, page restore/hide, and document visibility changes conservatively
invalidate an existing sample and cancel pending reads. This is not a claim that
sleep occurred or that connectivity failed. Once invalidated, a sample cannot
become current again through a clock change; an explicit fresh read is required.

Only one panel request is in flight. A six-second browser deadline cancels a hung
read, and late completions are ignored. The server retains its existing
five-second request context. OS metadata calls are still synchronous; this work
does not claim to make those kernel calls forcibly interruptible.

## Browser authority and projection

`GET /api/network-quality` is a fixed, authenticated, parameterless read through
`network-quality.local`. The existing strict Host/origin checks precede dispatch.
A browser session and GET/query/body validation precede native controller access.
Responses are `no-store`; fetch uses same-origin credentials, no cache, and
redirect rejection. There is no caller-selected interface, destination, URL,
probe, filesystem path, generic RPC method, or lifecycle operation.

The browser response is an explicit projection, not serialization of the native
DTO. It excludes scope IDs, logical sensor IDs, transient evidence IDs, native
free-form guidance, prefixes, addresses, raw OS flags/errors, credentials, and
any future native-only fields. Native source/method/state/metric/chronology
contradictions become a bounded unavailable error before serialization. The UI
chooses its own fixed gap explanations rather than treating arbitrary text as
authoritative instructions.

Frontend input is `unknown`. Parsing rejects extra fields, unsupported enums,
malformed or oversized interface values, invalid dates, conflicting metrics,
measured gaps, unmeasured administrative values, and inflated freshness. The
reader bounds decompressed response bytes to 8192 before parsing JSON. Neither
HTTP error bodies nor arbitrary thrown exception text is displayed.

## Evidence and limits

Controller/browser boundary fixtures cover authentication, Host/origin, method,
query/body rejection, minimization, validation, and safe errors. Frontend tests
cover parser failures, on-demand behavior, replay/freshness boundaries, clock
changes, focus/resume invalidation, timeout, late responses, demo isolation, and
core-view failure isolation. Linux process E2E follows real browser session →
web → authenticated UDS → controller → OS metadata before and after enrollment.
It checks that Device Watch stays off, monitoring history remains empty, and
closing web leaves the controller alive.

This is not real-browser accessibility certification, a macOS runtime test, or a
physical network test. The independent accessibility branch and hardware gates
remain separate. Active checks, durable quality history, and corroborated
diagnosis remain future work with independent scope, privacy, and traffic limits.

Implementation references: [React effect cleanup](https://react.dev/learn/synchronizing-with-effects)
and [monotonic time](https://www.w3.org/TR/hr-time-2/#monotonic-clock).
