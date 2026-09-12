# Native selected-HTTPS settings

Related issues: #14 and #29. The running controller exposes three authenticated
native commands for [durable HTTPS settings](https-configuration.md). Saving uses
the sole active enrolled LAN binding from storage. It does not inspect the current
route, resolve names, send traffic, enable Device Watch or grant execution consent.

## Local commands

With an enrolled network and a running controller:

```sh
cozysoc https-save --endpoint 198.51.100.20:443 --server-name check.example \
  --request-target '/check?test=1' --family ipv4 --method HEAD \
  --expected-status 204 --destination-policy exact-endpoint
cozysoc https-list
cozysoc https-retire SELECTION_ID
```

The endpoint and name above are documentation fixtures, not default destinations.
All seven settings are required. Endpoints use canonical numeric IPv4 or bracketed
IPv6 with port 443; family must agree. Methods are GET or HEAD. The server name is
an explicit DNS identity, stored lowercase, and the request target is an exact
origin-form path with optional query. The [review validator](https-review-plan.md)
defines the remaining addressing, identity, status and request bounds. No implicit
endpoint, DNS lookup, path, status or destination policy is supplied.

Each command accepts `--state-dir PATH` before positional arguments. The CLI talks
to the existing controller over its protected Unix socket; it does not open SQLite
or start a second controller. Save/list deliberately print private local settings
as escaped JSON, including endpoint, TLS name, request target, scope and immutable
references. Their result says `mode: configuration-only` and `consent_granted: false`.
Treat terminal output and shell history as private when using sensitive targets.

Retirement takes the exact opaque `https-selection` reference returned by save/list.
It is one-way and remains available after enrollment or route loss. It preserves
the record for attribution and does not free a lifetime slot. Saving identical
settings creates a new reference. After an uncertain mutation response, reload
settings before retrying; a lost response does not mean the write failed.

## Native boundary and evidence

The `network-quality.https-save`, `network-quality.https-list` and
`network-quality.https-retire` methods require verified kernel peer identity, the
rotating session secret and the matching API version. Requests use exact, unique,
bounded JSON fields; list requires an empty object. Caller-supplied scope, source,
interface, references on save, budgets and approval fields are rejected. Errors
return normalized messages without database details or private settings.

These commands are available on supported Unix controller platforms, including
macOS and Linux. No browser route, HTTPS plan/check/run/approval command, consent
ticket or collector is exposed. Enrollment and saved settings cannot establish
current route validity or network reachability. An internal [route inspector](https-route-inspection.md)
is available for future controller preflight. Actual HTTPS socket binding,
TLS/HTTP execution, enforced byte/deadline budgets and one-shot consent remain
future work.

Tests cover exact-field and authority rejection, authenticated socket/CLI round
trips, unavailable enrollment, retirement, and absent browser routes. A real
controller-process test saves settings, restarts, lists and retires them while
checking that no observation or gateway/resolver run audit was created and no
private settings reached controller logs. Synthetic enrollment is sufficient:
this evidence does not certify actual HTTPS traffic, TLS or packaged permissions.
