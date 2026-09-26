# External AdGuard Home read boundary

Related issue: #16. This candidate integration adds a controller-owned,
read-only API client, native macOS connection commands, separately approved
one-shot DNS observation from native or local browser review, and a local
browser status view. Device detail can show retained DNS events with a unique,
recent, time-valid Device Watch IP
association, marked as inferred. These remain candidate behavior until the
release gates are validated.

The client targets the documented v0.107.79 `/control` API. It reads status,
filtering state, query-log configuration and, when the service is running and
query logging is enabled, at most 100 recent query-log entries. It only uses
fixed GET paths. It rejects other versions, malformed responses, redirects,
non-JSON bodies, and responses above 2 MiB. Each request has a five-second
limit. Upstream errors and response bodies are never returned as diagnostics.
The endpoint must be explicitly selected and IP-literal, so a DNS change cannot
silently move credential-bearing requests. HTTPS is required except for a
loopback HTTP instance. Hostname support needs a separately validated pinning
and reapproval path. Credentials are supplied as protected
`secretstore.Secret` values, not URL userinfo. The client does not use proxy
environment variables.

`Probe` reads only status, filtering state and query-log configuration. It is
the validation path for a proposed connection and never retrieves query names
or client history. `Read` calls that same probe before its bounded query-log
read.

`cozysoc adguard-connect --endpoint https://IP[:PORT] [--username USER]`
requires a normal-user foreground macOS terminal. It discloses the exact
read-only scope and external ownership, flushes type-ahead, and requires an
explicit `connect` response. The optional password is read with terminal echo
disabled and is sent only to the authenticated, OS-peer-verified controller
socket. The controller probes the selected instance before saving enabled
external ownership intent. It persists the approved origin and an opaque
Keychain reference, not the password. `adguard-status` makes a fresh status-only
probe; it does not return query history. `adguard-disconnect` separately
confirms removal of Cozy SOC's local connection and credential, without
changing or stopping the external service. A failed Keychain deletion leaves
disabled local intent for a safe retry. All native results and errors omit
passwords and upstream response bodies. Browser routes do not expose these
controls. Authenticated `GET /api/adguard/status` is a separate, user-triggered
status-only read. Its bounded browser projection omits the configured username,
password, secret reference and query history. A deliberate admin link uses only
the validated IP-literal origin and opens the external owner's page in a new tab
without a referrer. A direct link is withheld when the resolver shares the
web UI's hostname: browser cookies ignore ports, so that navigation could
send Cozy SOC's HttpOnly session cookie to the other service.

`cozysoc adguard-collect ENROLLED_SCOPE_ID` requires a separate, exact terminal
approval for one read of at most the newest 100 query-log entries. The
authenticated browser Tools page can instead review the current external
origin, enrolled scope, interface, prefixes, 100-query bound, and 24-hour query
eligibility window, then
approve or decline a one-use review within five minutes. Browser approval is
bound to the reviewed origin and scope at the controller before private query
history is read. Expired, replayed, changed or unreviewed browser requests cannot
start collection. A failed or interrupted run never retries automatically; the
result is count-only, and an uncertain outcome directs the user to saved local
history. These browser routes cannot connect, disconnect, or configure AdGuard.
The controller verifies the selected enrolled network scope still matches the
current interface and prefixes before reading private query history. It stores
only entries with a visible client IP inside those prefixes. Anonymized,
missing-client-IP and out-of-scope entries are skipped; old or future entries
are skipped as well. The native result reports counts and whether the 100-entry
limit was reached, never names or client addresses. A limit hit means earlier
history may be missing. Repeated reads deduplicate the same source entries.
Requested and terminal collection phases are audited with scope and counts,
not query contents. A failed requested audit prevents the private read. There
is no background polling or automatic retry.

Status reports running, protection, filtering and query-log state separately.
Each transient query preserves its original timestamp, question, available
client address or ID, response status and filtering reason. An absent filtering
reason remains unknown. An anonymized client address is withheld because it
cannot establish device attribution;
even a present client ID describes only a request that reached this resolver.
These observations are private browsing data. The one-shot collection path
persists selected DNS name, client IP, query type, response status and filtering
reason in the controller's local evidence store with `ephemeral` retention
(24 hours by default); configured ephemeral retention may be shorter. Client
IDs are used only to form opaque deduplication keys and are
not stored in the observation payload. Query names and addresses never enter
index keys, native results, logs, or diagnostics. Retained events state
`device-identity-unverified`; they do not create device identity claims or
coverage samples. A matching prefix means only that the reported client IP
belongs to the selected scope, not that the resolver observed all its clients.
Device detail shows only the bounded, inferred matches; user-facing
retention/deletion controls remain separate work under #30.

The source contract is the [AdGuard Home v0.107.79 OpenAPI definition](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.79/openapi/openapi.yaml).
The API and connection paths are tested against local HTTP and in-memory
Keychain fixtures, including the canonical durable config manager. A live
v0.107.79 instance and signed macOS Keychain runtime exercise remain release
gates under #8 and #29. The one-shot collection path has local HTTP and store
round-trip fixtures, including deduplication and scope exclusions. This is
candidate support; live signed runtime, device-history validation, managed
configuration and coverage verification remain later #16 gates.
