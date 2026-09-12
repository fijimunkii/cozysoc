# Durable selected-resolver settings

Issue #14 now has controller-owned SQLite settings for the bounded UDP profile.
The [native controller commands](resolver-controller.md) now expose explicit
save/list/retire and preview operations. No browser mutation is available. [Native execution](resolver-consent.md)
requires a separate experimental controller opt-in and one-shot approval. Saving settings sends no traffic, enables no capability, grants no consent,
and stores no ticket, source address or route freshness. The product coordinator
must still perform fresh enrollment/route checks and one-shot review/consent.

`CreateResolverConfiguration` requires an existing unretired LAN scope and an
explicit numeric endpoint, fully qualified query, family, type, expectation,
transport and destination policy. The shared plan validator enforces supported
configuration without fabricating a source binding. Names are canonical lowercase.
The store allocates random selection, resolver and query references; caller-owned
references are rejected. Each creation gets new references, even for identical
settings. Changing settings means retiring the old record and creating a new one.
These are separate deliberate operations; a failed replacement leaves the old
record retired. No endpoint/name can be rewritten under an earlier reference.

`ListActiveResolverConfigurations` reloads up to 16 current settings for a scope,
including after a lost creation response. `ActiveResolverConfiguration` uses the existing separate read-only SQLite pool
with a one-second deadline. It returns only active settings on an active LAN
scope, validates persisted contents and reference consistency, and exposes private
values only through an explicit disclosure copy. A returned copy is not ongoing
authority: every execution preflight must resolve the reference again and validate
current enrollment membership, interface, source and route. An enrolled-prefix
policy cannot establish destination membership until that binding is available.

Schema 2 migrates both new and schema-1 databases in one transaction. Existing
rows and rollback-journal behavior remain intact. Creation and one-way retirement
use single writer statements with triggers that atomically insert redacted audit
records. A failed audit rolls back the settings change. This avoids a multi-call
transaction on the pinned writer absorbing unrelated run audits. SQL guards also
reject mutation, deletion, reference reuse and replacement of immutable records.
Audits contain opaque references, scope and state, never endpoint or query name.
The configuration itself contains private local endpoint/name data in the private
SQLite state directory; generic formatting and JSON serialization are redacted.
No normalized observation, coverage sample or run admission is created.

Bounds are 16 active records and 256 lifetime records per state directory, each
with at most 4096 bytes of configuration JSON, within the existing disk quota.
Retirement preserves attribution and does not release the lifetime slot. Automatic
retention removes configuration audits using the normal audit policy but never
recycles settings references or deletes private settings. Archive cleanup and a
user-facing removal flow remain future work; reaching a bound fails closed.
A lost write response requires reloading state, not assuming the write failed.
Configuration errors are normalized to avoid disclosing household values.

Tests cover schema-1 upgrade and failed-migration rollback, restart stability,
immutable attribution, replacement/reference rejection, concurrent active limits,
lifetime bounds, invalid settings, retired scope, canceled operations, clock
validation, redaction, and atomic save/retirement audit failure. They use synthetic
addresses and query names and do not establish network or hardware support.
