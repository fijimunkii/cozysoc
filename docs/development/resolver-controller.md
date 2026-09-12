# Native resolver settings, preview and controller ownership

Related work: #14 and #29. The controller now owns one resolver run coordinator
and exposes four narrow authenticated native methods. There is no resolver
execution/approval method or browser route yet. Neither controller startup nor a
settings operation creates consent or sends traffic. Device Watch enablement is
independent; only its explicitly enrolled network binding is used.

## Local commands

With an enrolled network and a running controller, configure a resolver explicitly:

```sh
cozysoc resolver-save --endpoint 192.0.2.53:53 --name test.example. \
  --family ipv4 --transport udp --query-type A --expect answer \
  --destination-scope enrolled-prefix
cozysoc resolver-list
cozysoc resolver-plan SELECTION_ID
cozysoc resolver-retire SELECTION_ID
```

These example addresses/names are documentation fixtures, not default targets.
Choose an endpoint and query appropriate to the network you are authorized to
check. Every setting is required, including UDP, expected answer and destination
policy. `exact-endpoint` permits only the configured numeric endpoint outside the
enrolled prefixes; it does not grant execution consent. Names require a trailing
dot and numeric endpoints require port 53. Each command accepts `--state-dir PATH`
before positional arguments. The CLI connects to the authoritative controller;
it never opens SQLite directly or constructs another coordinator.

Save/list print private local configuration as escaped JSON, including immutable
selection/resolver/query IDs. Saving has no route or network I/O. Retirement is
one-way, requires only the exact opaque selection ID, and remains available after
route or enrollment loss. An uncertain mutation response requires reloading state
before attempting another mutation. Existing settings limits and redacted atomic
audits are described in [durable settings](resolver-configuration.md).

Preview resolves a saved selection only. It reads native route/interface metadata,
then prints the exact endpoint/name, current source and interface, routable
prefixes, timestamps, DNS ceilings and upstream-forwarding caveat. It does not
reserve a run slot, create a ticket, reset cooldown, or record a measurement.
`execution_available` and `consent_granted` remain false. Native preview is
available on macOS when the route/source can be verified; unsupported platforms
fail without fallback. Settings storage remains usable on Linux.

## Authority and lifecycle

All four UDS methods require a verified kernel peer UID, rotating session secret
and API version. Parameter fields are exact, unique and bounded. Caller-supplied
scope/source/interface/budget/reference IDs on save and any approval field are
rejected. Only the selected reference can enter preview. No browser API or generic
proxy exposes these methods. Errors never include raw database or route details.

One inert coordinator is installed only after the controller owns its socket.
Installation has no settings read, route lookup or probe side effect. It cannot be
replaced per request, destination, configuration update or enablement change.
Shutdown closes admission and joins callbacks/audits before storage closes; an
unsuccessful drain cannot replace the coordinator or restore admission.

Every internal prepare/run preflight loads active settings and the sole enrolled
Device Watch scope, requires their IDs to match, and reads route metadata. It
then reloads both durable inputs and compares them with the exact inspected plan.
Retirement, scope/interface/prefix change, storage failure, stale route evidence,
clock reversal, cancellation or mismatched inspection returns no selection.
Freshness is never reset by previewing or by reloading settings.

Passive enrollment includes link-local neighbor prefixes. Resolver preflight
removes only those complete link-local ranges and canonically compares the full
remaining routable set with the native inspector. Link-local resolver endpoints
remain unsupported. IPv4/IPv6 source and prefix checks stay in the existing route
and plan validators; no address is inferred or resolved through system DNS.

Tests exercise real SQLite plus the authenticated UDS and CLI save/list/preview/
retire journey, with a synthetic route inspector and no DNS traffic. Regressions
cover exact-field rejection, missing/unsupported peer identity, private errors,
retirement during route inspection and between review/admission, scope changes,
stale evidence, startup quiet interval, singleton ownership, shutdown joining and
absent browser/execution routes. The native route/sender lab remains a separate
macOS gate; these tests do not certify physical networking or packaged permissions.

The next step is a connection-bound native consent protocol and interactive check
command, with server-held tickets, complete disclosure and explicit one-shot
approval. Existing settings and previews cannot be submitted as that authority.
