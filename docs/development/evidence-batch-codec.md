# Evidence batch storage codec

Related: #150. `storage.EncodeEvidenceBatch` and `storage.DecodeEvidenceBatch`
define a bounded format for retained evidence. They are not
used by live persistence or history readers. No database migration, runtime
flag or storage-policy change is introduced. The [standalone prototype](device-watch-persistent-batch-prototype.md)
remains separate evidence; its 22.42 MiB result does not measure this codec with
production expiry metadata or establish the controller's budget.

## Evidence and retention

Each record contains an optional original observation with its stored expiry,
identity claims with their individual stored expiries, and device links. An
observation may be absent while claims or links remain, allowing the integration
to represent independently retained evidence without keeping an expired payload.
The codec does not remove or renew anything: callers must apply existing retention
and foreign-key semantics and pass the resulting original records and expiries.
It never reads the current clock or recalculates a TTL.

Observation payloads are encoded separately as bytes so their original JSON
whitespace and content survive. Nil pointers, original timestamps, confidences,
provenance and links remain distinct. The codec preserves supported noncanonical
IDs in full. It never re-runs identity reconciliation or substitutes a current
identity decision for historical evidence.

## Format v1

The gzip payload is a canonical JSON frame marked `cozysoc-evidence-batch`, version
1. Each record explicitly identifies whether claim/link IDs use the frozen
Device Watch derivation or are stored in full. Derivation is selected only when
all original IDs and claim references match exactly. The encoder works on copies;
the decoder reconstructs the same IDs before validating the records.

The format marker distinguishes this codec from the test-only prototype format;
prototype databases are not product databases and are not migrated by this change.
A checked-in v1 frame and fixed expected IDs guard compatibility. A separate test
uses actual Device Watch observation construction and reconciliation to verify
compatibility with producer IDs. Future producer or domain-model changes must
retain this v1 decoder and its stored-data contract; do not simply regenerate the
golden fixture to accommodate incompatible output. Add a new codec version when
the stored representation changes.

## Bounds and failure behavior

- At most 100 records per batch and eight claims/eight links per record.
- At most 1 MiB compressed and 1 MiB decompressed frame data; payload bytes also
  obey the domain's 64 KiB limit, including original surrounding whitespace.
- Before decoding record structs, a token pass rejects arrays over 100 elements
  and nesting deeper than 32 levels. Domain/association validation then applies
  the more specific record bounds. This avoids large struct allocations from
  tiny repeated JSON objects.
- Unknown formats/versions/fields, duplicate fields, alternate field spellings,
  trailing JSON or gzip streams, truncated data and bad checksums are rejected.
- Missing required expiry metadata, invalid domain records and orphan observation
  expiry metadata are rejected. No partial records are returned on failure.

`ErrEvidenceBatchLimit`, `ErrEvidenceBatchFormat` and `ErrEvidenceBatchData` allow
callers to distinguish capacity, encoding and record errors without logging
private field values. A writer may start a new batch when an append exceeds a
limit, but it must not acknowledge, drop or truncate evidence that failed to encode.
These limits define this codec's supported shape; integration must preserve other
supported legacy records through an explicit compatible storage path.

## Validation and remaining integration

Tests cover independent expiry, absent observations with retained claims/links,
canonical and noncanonical IDs, payload bytes, input ownership, golden-format
compatibility, real-producer compatibility, corrupt/ambiguous frames, structural
bounds and no-partial-result behavior. Fuzz targets exercise compressed bytes and
raw frame mutations followed by valid gzip wrapping; accepted records must always
encode and decode again without change.

## Reconciliation staging

Device Watch's `PlanReconciliation` accepts an identity reader and returns the
complete provisional claims, links, optional new device and reconciliation result.
It performs one scoped recent-MAC lookup without writing evidence or assigning
storage expiry. The seven-day continuity horizon, source-time fallback, ten-minute
validity, confidence values and versioned IDs remain the existing producer rules.
Ambiguous continuity retains claims without inventing a device or links. Failed
validation or lookup returns no partial plan.

The live reconciler uses this planner before its existing individual storage writes.
It preserves original claim IDs returned by legacy idempotent insertion and derives
links from those resolved IDs. A lookup failure now occurs before any new claim
write. This refactor does not make live ingestion atomic or activate batch storage.
A batch transaction owner must check source-key replay first, plan against that
same transaction's identity snapshot, assign each stored expiry, persist all related
changes, and acknowledge only after commit. Plans must not be queued and applied
against a later identity state. Query integration and migration remain required.

`storage.NewLegacyIdentitySnapshot` supplies the legacy-table identity reader for
an exclusively owned SQL transaction and fixed retention-evaluation time. It shares
the live store's query implementation, including normalization, inclusive observed
time bounds, retained-claim expiry, retirement at the query's upper bound, distinct
device ordering and the three-candidate ambiguity limit. Claim/link validity is not
substituted for the existing recent-evidence continuity rule. Queries see the
transaction's own staged changes and fail once it is committed or rolled back;
the reader never owns transaction completion or falls back to another connection.

This is only the legacy half of mixed-history reconciliation. It must not be used
alone once batch-only claims are written. Batch identity indexes and a combined
reader remain necessary. Snapshot tests cover staged ambiguity and retirement,
external read isolation, commit/rollback, cancellation, clock and query validation,
expiry/time boundaries, scope filtering and planner use of the same transaction.

Tests exercise read-only new/continuous/ambiguous decisions, IPv4/IPv6 normalization,
original evidence and codec round trips, lookup failure, and legacy partial-write
recovery/replay with noncanonical claim IDs.

## SQL transaction primitives

`evidence_batch_sql.go` supplies internal append and scoped point-read primitives
using this codec. Its schema is reserved and is **not installed by `Store.Open`**;
no live writer or reader uses it. It accepts an exclusively owned SQL transaction,
so the integration can commit evidence, indexes and related changes together.
The transaction owner must roll back any error and acknowledge only after a
successful commit. It must use a dedicated connection, never the shared pinned
`Store.conn`. Tests use a separate pool against the same private SQLite file,
with foreign keys and FULL synchronous durability enabled.

Each batch belongs to one sensor/stream and enrolled scope. Appends validate the
sensor's scope and require a complete observation bundle whose claims and links
refer to that observation. The codec's broader retained-only shapes remain valid
encoding, but are not fresh append inputs. The latest partial batch is rewritten
within the transaction and rolls over at either codec bound. The source-key
uniqueness contract matches observation ingestion: a replay of the same
sensor/stream/source key keeps the first evidence and expiry, even if the replay
has another observation ID. An observation ID reused for a different source key
is rejected. Canonical observation IDs and source keys use tagged reversible
binary digests; other values remain in full, without hashing. Equal packed source
keys and IDs share the primary lookup entry. Only keys that differ from the packed
observation ID occupy the partial replay index. Replay checks use the primary key
and that partial index; they do not scan all lookups. Database insert/update
triggers enforce source-key uniqueness across the two representations, while the
partial unique index enforces it between exceptional keys. Replays retain the
same first-record behavior regardless of which representation was stored first.

Point reads require a scope, observation ID and explicit evaluation time. They
return the original bundle only before the stored observation expiry, validating
payload size, slot/count, ID, source, scope, key and expiry against the index.
Returned claims retain their individual stored expiries; this is a raw evidence
read, **not a current identity or presence projection**. Missing and expired
lookups return `sql.ErrNoRows`; corrupt evidence returns no partial record.

Regression tests cover replay, ID conflicts, source and scope separation, exact
expiry boundaries, independently expired claims, reopen, count/byte rollover,
corrupt payload/index rejection, injected rollback and separate transaction
ownership. These primitives do not yet implement full identity foreign keys and
query indexes, pagination, quota recovery, connection lifecycle,
schema migration or rollback compatibility. They cannot be activated until those
contracts and the legacy-record fallback are implemented and tested. Re-measure
the unchanged full controller workload, including all database pages and
expiry/index overhead, before claiming #150's storage target. CPU/RAM and sustained-run gates remain open.

## Batch retention

The internal `pruneEvidenceBatches` primitive selects at most 100 due batches
through an index on each batch's earliest original observation/claim expiry.
Each batch remains bounded to 100 records and the codec byte limits. Selection,
rewrites, empty-batch deletion and lookup remapping share the caller's exclusive
transaction. Counts are provisional until commit; an error requires rollback and
returns no partial counts. Live `Store.PruneExpired` does not call this primitive.

At the exact stored expiry, an observation payload and its source-key lookup are
removed. Surviving claims and links keep their original IDs, times, confidence and
other evidence, with observation references cleared to match the existing
SQLite `ON DELETE SET NULL` behavior. Expiring a claim also removes its links,
matching `ON DELETE CASCADE`. The next expiry is calculated from surviving stored
expiries, never a fresh TTL. Partial batches with retained-only records can accept
new complete records; slot compaction updates all surviving observation lookups.
A replay remains suppressed while its observation lookup exists, and can be
accepted again after that observation is pruned, as with legacy storage.

Pruning validates the original lookup rows against the decoded records before
rebuilding them. Missing or corrupt slots, keys, sources and expiry metadata abort
the transaction rather than being silently repaired. Retained records are
repartitioned if re-encoding exceeds a codec limit; evidence is never truncated to
make it fit. Deleted SQLite pages can be reused; this primitive does not shrink
the file with `VACUUM`, implement quota recovery or publish retention audit events.
Those remain responsibilities of the controller integration.

Regression coverage compares observation-reference clearing and claim/link
cascades against the legacy SQLite tables. It also covers original derived IDs
after observation deletion, independent expiry boundaries, reopen, appending to a
pruned batch, lookup compaction, empty batches, bounded selection, cancellation,
corrupt-index rejection and rollback across multiple partially processed batches.
