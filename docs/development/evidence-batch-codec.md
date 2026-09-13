# Evidence batch storage codec

Related: #150. `storage.EncodeEvidenceBatch` and `storage.DecodeEvidenceBatch`
provide the reusable codec for the next storage integration step. They are not
called by live persistence or history readers yet. No database migration, runtime
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

The codec does not implement durable acknowledgment, source-key idempotency,
query indexing/pagination, scope filtering, pruning, quota recovery or migration.
Those belong in the storage adapter and must preserve current contracts before
live writes or readers use this format. Re-measure the unchanged full controller
workload, including all database pages and expiry/index overhead, before claiming
#150's storage target. CPU/RAM and sustained-run gates remain open.
