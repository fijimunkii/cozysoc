# External AdGuard Home read boundary

Related issue: #16. This candidate connection slice adds a controller-owned,
read-only API client and native macOS connection commands. It does not yet ingest
durable DNS evidence or expose DNS activity in the UI. Those remain necessary
before the broader read-only integration can be claimed as supported.

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
controls.

Status reports running, protection, filtering and query-log state separately.
Each transient query preserves its original timestamp, question, available
client address or ID, response status and filtering reason. An absent filtering
reason remains unknown. An anonymized client address is withheld because it
cannot establish device attribution;
even a present client ID describes only a request that reached this resolver.
These observations are private browsing data. The client neither logs nor
persists them. Future storage and UI consumers must apply explicit retention,
redaction, identity and coverage contracts before publication.

The source contract is the [AdGuard Home v0.107.79 OpenAPI definition](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.79/openapi/openapi.yaml).
The API and connection paths are tested against local HTTP and in-memory
Keychain fixtures, including the canonical durable config manager. A live
v0.107.79 instance and signed macOS Keychain runtime exercise remain release
gates under #8 and #29. This is candidate support; query ingestion, device
attribution, coverage, retention and admin deep links remain later #16 slices.
