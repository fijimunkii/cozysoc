# Durable selected-HTTPS settings

Related issues: #14 and #29. The controller now owns private SQLite settings for
[explicit HTTPS review plans](https-review-plan.md). Save, list, resolve and retire
storage methods underpin the [native settings commands](https-controller.md).
They add no traffic, monitoring enablement, execution approval, route sample or
network-quality observation. No browser endpoint exposes these settings.

## Immutable settings and attribution

`CreateHTTPSConfiguration` requires an existing unretired LAN scope and explicit
numeric endpoint, TLS server name, request target, family, method, expected status
and exact-endpoint policy. The review validator checks this configuration without
inventing a source or route. Creation cannot precede enrollment time. Names are
stored in canonical lowercase; the exact escaped request target is preserved.

The store allocates random `https-selection`, `https-endpoint` and `https-request`
references with 128 random bits each. Caller-supplied references are rejected.
Every creation receives new references, including identical settings. A setting's
scope, profile, endpoint/request references, configuration and creation time cannot
change. Replacing it means explicitly retiring the old record and creating a new
one. Those operations are separate; a failed replacement leaves the old record
retired. SQL guards reject deletion, replacement, reference reuse, unretirement and
other mutation, preserving the original attribution of later retained evidence.

The `selected-https-v1` profile is persisted separately from configuration. Reads
validate the profile, reference grammar, column/payload consistency, enrollment
chronology and canonical JSON before returning any data. Unknown, duplicate,
case-aliased or inconsistent fields fail the entire settings read, not a partial
list. Stored JSON intentionally avoids HTML escaping so every permitted 1024-byte
request target fits the 4096-byte record bound. This encoding belongs only in the
protected SQLite table; it is not an HTML or browser projection.

`HTTPSConfiguration` is redacted in ordinary formatting and JSON. Its explicit
`Disclosure()` returns a configuration copy whose private fields may be accessed
for local review; that configuration also remains redacted under ordinary JSON.
Only deliberate conversion to `httpsplan.DisclosedConfiguration` produces private
serialized data. No ticket, challenge, source address, route freshness, credential
field or response content is stored with these settings.

## Atomic audit and bounded reads

Creation and one-way retirement use one writer statement each. SQLite triggers
insert the corresponding redacted audit atomically; an audit failure rolls back
the setting change. No separate writer transaction can absorb unrelated run audits.
Audits contain only profile, state, scope and immutable references, never the exact
endpoint, TLS name or request target. A lost write response calls for reloading
state, not assuming failure and creating an automatic replacement.

`ActiveHTTPSConfiguration` and `ListActiveHTTPSConfigurations` use the existing
separate `mode=ro` pool, with a one-second total read deadline including queueing.
They resolve only active settings on an unretired LAN scope. A previously returned
copy is not continuing authority: every future preflight must reload settings and
verify current enrollment, interface, source and route. Retirement remains possible
for cleanup after scope retirement; it cannot reenable a setting.

Bounds are 16 active and 256 lifetime HTTPS records per state directory, each at
most 4096 bytes, within the existing SQLite disk quota. Resolver settings retain
their own existing bounds. Retirement preserves the private record and does not
free a lifetime slot. Normal audit retention can remove configuration audits but
never deletes or revives settings or recycles references. A user-facing archive
removal/privacy-cleanup flow remains future work; reaching a bound fails closed.

## Schema migration and evidence

Schema 3 upgrades new, schema-1 and schema-2 databases in one transaction. A failed
later step also rolls back any earlier migration step. Existing resolver settings,
audits, enrollment, pinned writer ownership and rollback-journal mode are preserved.
No new configuration is synthesized during migration.

Tests exercise both upgrade paths and rollback, preservation of schema-2 resolver
settings, restart/list/retirement behavior, atomic audit failures, concurrent active
limits, lifetime retention, maximum request size, strict decoding, private-data
redaction, canceled and queued reads, reader/writer isolation and invalid clocks.
These are storage/fixture tests, not external networking or packaged support evidence.
Durable settings now support the next native review/route/consent work; actual HTTPS
collection and byte/deadline enforcement remain unimplemented.
