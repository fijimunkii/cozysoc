# External AdGuard Home read boundary

Related issue: #16. The first AdGuard Home code is a controller-internal,
read-only API client. It does not yet create a saved connection, ingest durable
evidence, or expose DNS activity in the UI. Those are required before claiming
the read-only integration is available to users.

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
This package is tested against local HTTP fixtures; a live v0.107.79 instance,
credential storage flow and user-visible connection still need validation.
