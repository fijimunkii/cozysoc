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
alone once batch-only claims are written. The combined reader described below adds reserved batch identity routes; it
remains separate from live activation. Snapshot tests cover staged ambiguity and retirement,
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
encoding, but are not fresh append inputs. The latest partial batch with the same identity lookup keys is rewritten
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
ownership. These primitives do not yet implement full identity foreign keys,
live history-reader wiring, retention-driven quota recovery,
schema migration or rollback compatibility. The queue owner below supplies a
private configured connection and legacy replay dispatch. Live activation still
requires the remaining contracts to be implemented and tested. Re-measure
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

## Identity routing and combined queries

The reserved adapter groups records by their source and exact normalized set of
linked `(claim kind, claim value, device ID)` tuples. The sorted routing dictionary
is stored in full, without lossy hashes; route order and duplicate links do not
change the key. Records with different lookup keys use separate partial batches.
Claims, links, observations, original payload bytes and individual expiry values
remain in the v1 codec. A group is a lookup aid, not a claim of continuous presence.
Unlinked ambiguous claims remain stored even though they have no device route.

Each batch stores the minimum/maximum original claim observation time and maximum
claim expiry for candidate selection. These broad bounds never establish an event
inside a gap or allow one claim to borrow another claim's expiry. Candidate reads
verify the original batch, routing dictionary and time metadata, then check exact
claim kind/value, observation time, expiry and device link. They retain the legacy
retirement rule and do not substitute presence validity for recent continuity.

`NewMixedIdentitySnapshot` combines the legacy reader and reserved batch reader in
one caller-owned transaction, deduplicates devices, sorts by ID and returns at most
three candidates. Missing schema and corrupt selected data fail the entire query;
there is no fallback that silently omits batch history. A query inspects at most
100 candidate batches, each bounded by the codec limits. Exhaustion returns
`ErrEvidenceBatchQueryLimit` with no partial candidates, never false absence or
uniqueness. This work limit is not a measured latency or CPU guarantee.

Appending and pruning validate existing routing and bounds before mutation.
Pruning regroups records if independent claim expiry changes their surviving keys,
rebuilds observation slots and bounds in the same transaction, and removes unused
dictionaries and routes. It does not retain expired lookup values in empty groups.
The schema remains reserved: live migration, ingestion, history/detail/activity
queries, complete foreign-key behavior, quota/lifecycle integration and rollback
compatibility are still required before activation.

Regression coverage includes unchanged evidence under interleaved grouping,
normalized routing, exact-time gaps, independent expiry, mixed-format ambiguity and
deduplication, scope, closed transactions, corrupt routing/bounds, rollback,
retention regrouping and cleanup, indexed candidate selection, and work-limit
failure without a partial identity decision.

## Transactional ingestion staging

`NewEvidenceBatchStager` copies the configured retention policy and accepts the
trusted controller planner adapter `devicewatch.PlanBatchEvidence`. `Stage` uses
an exclusively owned caller transaction: it validates sensor/scope authority and
checks legacy and batch source-key replay and observation-ID conflicts before
invoking the planner. A missing reserved schema is an integration error.

Batch replay returns an empty record and `false` without invoking the planner,
reading the clock, assigning new expiry or rewriting data. Expired but unpruned
observations still suppress insertion. Legacy replay instead returns
`ErrEvidenceBatchLegacyReplay`: the owner must use the compatibility/repair path
with the original stored observation. A legacy observation may have been committed
before reconciliation completed, so acknowledging it as a complete atomic batch
could silently skip missing claims or links. Live compatibility handling remains
an activation requirement.

For a new observation, the planner reads the same transaction through the mixed
identity snapshot. Observation bytes and pointer fields are copied so the planner
cannot rewrite original evidence. Storage assigns independent write-time expiry
to the observation and each claim. It validates the complete bounded bundle,
stages any referenced new device without overwriting existing creation evidence,
checks that every linked device exists, and appends the bundle and all indexes in
that transaction. Ambiguity may retain claims without a device or links.

The returned record and inserted flag are provisional. The owner must roll back
any error and acknowledge only after successful commit. The stager never opens,
commits or rolls back a transaction and must not use shared `Store.conn`. It does
not configure connection quota/durability, provide full mixed-format claim/link
uniqueness or deletion semantics, install migrations or activate the live queue.

Tests cover source replay in both formats, explicit legacy repair signaling,
unpruned expiry, ID conflicts, sensor/scope separation, missing schema, source-ID
zero separation, immutable original inputs, copied retention policy, independent
expiry, invalid plans, absent devices, ambiguity, injected late-write rollback,
retry and caller-owned commit. The adapter workload smoke test uses the real
planner through this stager; the recorded daily footprint remains the separately
identified earlier source measurement.

## Legacy replay repair

`devicewatch.RepairLegacyObservation` handles the stager's legacy replay signal in
an exclusively owned caller transaction. `NewLegacyRepairStore` resolves the
retained original using its scope/sensor/stream/source-key tuple and validates the
sensor's scope. Retry IDs, payloads and timestamps never replace stored evidence.
Missing or expired originals return `false` without recreating observations or
claims. Reads bound payload and metadata allocation and validate the resulting
original before reconciliation; corrupt evidence returns an error.

The repair store uses the combined identity snapshot and the same idempotent SQL
helpers as the live legacy store. Existing claim IDs and expiry are preserved;
missing claims and links can be completed, including after a partial earlier write.
Claim and link writes are confined to the original observation and its provenance.
The real reconciler supplies the original decision rules, including ambiguity and
resolved legacy claim IDs, without duplicating its identifier derivation in storage.

Repair never updates the original observation, commits, or acknowledges ingestion.
The owner must roll back failures and commit before acknowledgment. Tests cover
changed retry payloads/IDs/times, partial noncanonical claim IDs, preserved original
payload/expiry, owner-only commit, late-write rollback and retry, expired/missing
originals, wrong scope and corrupt or oversized stored data. The queue owner below
dispatches repair in its transaction. Runtime wiring, migration and the remaining
batch-reader/foreign-key/lifecycle gates are still required before activation.

## Queue and connection ownership

`NewEvidenceBatchIngestor` connects the existing bounded queue to the stager and
trusted producer repair adapter. It requires the reserved schema and does not
install it. The live runtime still uses the legacy ingestor until the remaining
reader, referential-integrity, retention/lifecycle and migration gates are ready.
A runtime using this constructor must use `StorageSink` without an outer
`ReconcilingSink`: successful observation receipts already include reconciliation.

The queue owns a separate pinned SQLite connection opened against the existing
literal file path (`mode=rw`). It applies the same rollback journal, FULL
synchronous durability, foreign keys, busy timeout, trusted-schema setting and
page quota as the parent store. Applying the quota to this connection prevents
new writes from bypassing the configured database size limit. Parent autocommit
writers and history snapshots cannot join its transactions.

Each Device Watch observation runs replay validation, planning or legacy repair,
evidence/index/device writes and any checkpoint update in one transaction. Only
successful commit produces a successful receipt. Replay still advances its
checkpoint, without changing original evidence or claiming a new insertion.
Errors roll back all staged work and retain the existing ingestion failure and
storage-full health reporting. Other observation kinds keep their legacy SQL
representation, with replay/conflict checks across both formats and a checkpoint
in the same transaction. Changing the kind on a retry cannot create duplicate
evidence or bypass legacy Device Watch repair. Other ingestion kinds retain their
existing SQL behavior on the private writer. This does not add automatic batch
pruning or resolve the remaining mixed-format uniqueness/deletion contracts.

Queue capacity, input copying, submission backpressure, receipt waiting and
processing deadlines use the existing ingestor. Close rejects new submissions,
drains accepted work and queued failure events, then closes its private connection
before reporting completion. A caller's timed-out Close does not interrupt that
drain or leak the writer; callers must close the ingestor before its parent store.

Tests cover private connection settings and closure, schema/adapter prerequisites,
queue overflow, failed and timed-out waits, draining, planner and checkpoint
failure rollback, failed commit followed by retry, SQLite quota exhaustion and
recovery, batch and legacy replay, original evidence and checkpoint preservation,
other observation fallback and reopened committed rows. A four-collection,
100-device collector run through `StorageSink` verifies 400 original observations,
800 claims, 800 links, 100 devices and four coverage samples, with no ingestion
losses. This is a short integration check, not a new daily footprint measurement or
a complete controller resource-budget result.

## Mixed device detail

`MixedIdentitySnapshot.GetDeviceEvidenceDetail` reads legacy and batch identity
history in the same caller-owned transaction with a fixed retention clock. It
keeps the existing device-detail output, scope and retirement rules: original
claim time and link start must be at or before the requested as-of time, and the
claim must still be retained at evaluation time. Presence validity ending does
not erase historical detail. Source observation provenance is included only while
that original observation remains retained; independently retained claims still
appear after observation expiry or pruning.

Legacy detail SQL is shared with the live `Store` reader. Results from both formats
are combined by claim time descending, then claim ID ascending; link ID provides
a stable tie-break for multiple links to the same claim. The summary uses the
latest eligible claim and the device's original creation time. One extra row
establishes truncation. Claim values match legacy normalization, while stored
batch evidence remains unchanged. Duplicate link IDs among examined eligible
batch rows or combined candidates return an error; this is not a substitute for
the remaining global mixed-format uniqueness contract.

A covering `(device_id, group_id)` routing index selects candidate batches without
creating an index entry per historical claim. Candidates are inspected by newest
claim bound. Each selected batch is decoded and checked against its source,
identity dictionary, lookup slots, independent expiry and claim bounds before
projecting original claims and links. Equal-time candidates are all considered;
a verified older upper bound can end the scan only when it cannot change the
requested rows or truncation. At most 100 candidate batches are decoded. Corrupt
selected evidence, schema/query errors or work-budget exhaustion produce no
partial detail or false not-found result.

Parity tests compare mixed and all-legacy results across observation times,
independent expiry boundaries, limits and pruning. Other tests cover retirement,
staged snapshot state and rollback, closed transactions, corrupt payload/index
metadata, duplicate selected link IDs, equal-time ordering, bounded work and the
device routing index. The real queue/collector fixture also verifies four original
collections and eight claim/link detail rows for each of 100 devices.
The index changes the reserved schema; previous footprint
measurements retain their exact source and do not measure this index. Live device
history reader wiring, lifecycle and migration gates remain
open, as does the unchanged full-controller storage measurement.

## Mixed device lists

`MixedIdentitySnapshot.ListDeviceEvidence` combines legacy and batch summaries in
one fixed snapshot. It preserves device-ID ordering, the exclusive `AfterID`
cursor, limits of up to 200 devices, and one extra device to establish the next
page. Devices represented in both formats appear once, with the latest eligible
original claim time. Scope, retirement and independent claim expiry match the
legacy list. Unlike detail and activity, the existing list contract does not
filter on link start or presence validity; the mixed list preserves that rule.
Observation expiry does not remove independently retained claim history.

The list uses an ordered device-ID range scan, indexed device-to-group and
group-to-batch lookups, and the same selected-batch validation as detail reads.
The join order keeps SQLite from scanning all retained batches per cursor step. It inspects original claims and links before accepting membership
or last-seen. Newest claim bounds order work, but never become displayed times.
Once a verified match reaches the current batch's upper bound or the as-of time,
older batches cannot increase that device's last-seen. A legacy page lookahead
also excludes batch device IDs that cannot affect the requested page.

A whole page decodes at most 1,024 candidate batches, including ineligible
candidates. Corruption in selected evidence, schema/query errors and work-budget
exhaustion return no partial page or cursor that skips unseen evidence. The reader
owns no transaction, writes no data and does not open or close connections.

Tests compare mixed and all-legacy pages across limits, cursors, historical times,
retirement, expiry and observation pruning. They cover future aggregate bounds,
selected corruption, exhausted work budgets, indexed query plans, page-boundary
selection and staged
snapshot rollback. The real 100-device/four-collection queue fixture checks
pagination with no duplicate or missing devices and exact last-seen times. These
checks do not activate live readers or establish the full controller budget;
referential integrity, lifecycle, migration and runtime reader wiring remain
required.


## Mixed device activity

`MixedIdentitySnapshot.ListDeviceActivity` combines retained legacy and batch raw
history per device before classifying first observations, address changes and the
latest observation. The seven-day predecessor window and 24-hour display window
match the legacy reader. Address changes compare consecutive addresses within the
same address family, including predecessors outside the display window. Original
claim times, source provenance, independent observation and claim expiry, link
start, scope and device retirement determine eligibility. Presence validity does
not erase historical activity. The per-observation MAX aggregates and equal-time
ranking retain legacy semantics; final ties use device ID for deterministic order.

Both formats are read in the caller's fixed transaction and retention clock. The
reader uses the existing device routing index and validates selected batches,
lookups, routing and bounds before projecting evidence. It writes no data and owns
no connection or transaction. Duplicate original observation IDs for one device,
selected corruption, schema errors and exhausted work budgets return no partial
activity page. The result retains at most the requested limit plus one candidate
to establish truncation.

A query examines at most 1,024 candidate devices, decodes at most 32,768 batches
and projects at most 2,097,152 raw rows. Each device's history is capped at 32,768
rows and 16 MiB of charged projection bytes before classification. These are
logical work and allocation bounds, not measured process RAM or CPU results.
The full seven-day, 100-device history performance and complete controller
resource and lifecycle gates remain unmeasured.

Parity tests compare mixed and all-legacy activity across first observations,
cross-format address changes, dual-stack history, ties, limits, scope, retention,
pruning and retirement. They also cover snapshot rollback, closed transactions,
corrupt selected evidence, duplicate originals, exhausted work budgets and indexed
query plans. The real four-collection, 100-device queue fixture verifies stable
latest activity. Activity, list and detail projections return UTC timestamps like
the legacy SQL readers; a regression verifies stored original timestamp offsets
remain unchanged.

This reader adds no schema or index and does not activate live batch history.
Live reader wiring, full mixed-format uniqueness and deletion, retention and
quota lifecycle, migration/rollback and runtime wiring remain required. Earlier
daily footprint evidence retains its exact measured source; the unchanged full
controller workload must measure the integrated result against 30 MiB/day.


## Mixed scope device membership

`MixedIdentitySnapshot.ListDevicesForScope` preserves the legacy scope query's
claim and link validity intervals. A retained claim must have been observed by
the requested as-of time, and its validity end must be absent or at least that
time. The linked device must match, the link must have started, and its validity
end must also be absent or at least that time. Claim expiry uses the fixed
retention clock; device retirement is inclusive at the as-of boundary. Original
observation expiry does not erase independently retained claims. Historical
evidence alone does not establish scope membership or fill presence gaps.

The reader shares legacy SQL, indexed batch selection and selected-batch
validation with the history device list. It continues past ineligible batches
and stops scanning a device after an actual valid claim/link match. Sorted device
IDs are deduplicated across formats, with the exclusive cursor and one extra
device preserving pagination. The same 1,024-candidate budget bounds a whole
page; corruption, query failure and exhausted work return no partial result.
The caller owns the transaction and fixed retention clock. No new index, schema
migration or live runtime wiring is added.

Tests compare legacy and mixed/all-batch results across both validity ends, future
link starts, unbounded intervals, gaps, retirement, retention, observation pruning,
limits and cursors. They also cover selected corruption, work exhaustion, older
valid evidence below a newer invalid batch, staged state, cancellation and closed
transactions. The real 100-device collector fixture verifies paginated scope
membership and its disappearance after the collection validity interval ends.
Full mixed-format referential integrity and lifecycle, migration/rollback,
runtime reader wiring and the complete controller resource
measurement remain required.


## Mixed observation history

`MixedIdentitySnapshot.ListObservations` merges original legacy and batch
observations in the caller's fixed transaction and retention clock. Scope,
optional sensor/kind filters, the inclusive ingestion-time window, original
observation expiry, limits up to 200 and the descending-time/ascending-ID cursor
match the legacy history reader. Source event times and claim times do not order
observation history. Payload bytes and provenance remain original; returned
ingestion/source timestamps use the legacy UTC representation without changing
stored timestamp offsets. Independently retained claims do not make an expired
observation reappear.

The reserved schema stores each batch's first and last original ingestion time
and maximum observation expiry. An ingestion-time index supports ordered candidate
selection across identity groups; append and pruning write these bounds with
payloads and lookup changes in the same transaction. Pruning recomputes bounds
from surviving observations, including zero bounds when only claims remain.
Existing persisted bounds must validate before append or pruning can rewrite a
batch. These fields and this index are not included in earlier exact-source
footprint measurements. The schema is still reserved and not installed by live
migration.

History candidates use ingestion bounds only for selection. Every selected batch
passes codec, source, routing, lookup, independent expiry and original-time-bound
validation before projection. Original observations decide filter membership and
ordering. All equal-time candidates are considered before truncation; a validated
older upper bound permits an early stop only when it cannot change the page or
its next cursor. Examined duplicate original IDs across formats return an error.

Each query decodes at most 1,024 candidate batches and retains at most the limit
plus one observation after each batch. Retained projections are charged against
16 MiB; mixed legacy reads additionally bound metadata and individual raw payload
materialization to 1 MiB. The fixed candidate/codec limits also bound temporary
decoding and examined-ID tracking. Corruption, schema errors, oversized legacy
projections and exhausted work budgets return no partial page. These logical
bounds are not measured process RAM, CPU or sustained-run results.

Tests compare mixed/all-batch history with legacy results across overlapping
batches, ingestion ties, source and claim times outside the query window, filters,
cursors, expiry and pruning. Other tests cover selected corruption, duplicates,
index plans, work and projection budgets, transactional bound updates, cancellation
and snapshot isolation. The real queue/collector fixture verifies pagination of
400 original batch observations together with its existing legacy observation.
Live reader wiring, full mixed-format uniqueness/deletion, retention and quota
lifecycle, migration/rollback and the unchanged full controller workload against
30 MiB/day remain required.


## Batch claim constraints and legacy ID conflicts

The SQL adapter validates each retained bundle against the legacy normalized
`(source_observation_id, kind, value)` uniqueness rule. While an original
observation is present, different claim IDs cannot carry the same normalized
kind/value for that source. Reads, append and pruning reject invalid retained
bundles without silently merging evidence. After observation removal, the source
reference is null and repeated normalized values remain permitted, matching
SQLite's null-source uniqueness semantics.

After resolving observation replay, a new batch append probes existing legacy
claim and link primary keys before writing identity routing or payload data.
An occupied ID is rejected even when the legacy row has expired but has not been
pruned. These bounded primary-key checks require no new schema or index. The
transaction owner rolls back all staged changes on error, including any proposed
new device. Tests verify normalized aliases, null-source behavior, expired rows,
claim/link conflicts, rollback and retry, and rejection of corrupt stored bundles
by reads and pruning. Retention parity uses separate legacy and batch databases;
reader corruption fixtures explicitly introduce legacy conflicts after append.

These checks enforce incoming-batch conflicts with existing legacy rows. They do
not establish global uniqueness between arbitrary batch records, guard later
legacy writers, or implement explicit claim deletion across formats. Device
deletion uses the transaction primitive below. Global uniqueness, retention/quota
lifecycle, migration/rollback, runtime activation and full-controller resource
verification remain required.


## Transactional mixed device deletion

`DeleteEvidenceBatchDevice` removes a global device and its links from both
storage formats in an exclusively owned caller transaction. The existing device
routing index selects affected batches across all scopes, without filtering out
expired but unpruned evidence. Every selected payload, routing group, lookup and
time/expiry bound must validate before rewriting. Original observations, claim
IDs, claim values, expiry and source provenance survive; only links to the target
device are removed. Links to other devices remain intact.

Deletion shares pruning's transactional repartition/rewrite path. It rebuilds
routing groups, lookups and bounds, preserving original IDs even when compact
ID encoding is no longer applicable, and removes unused routing dictionaries.
After batch rewrites, deleting the device cascades its legacy links through
SQLite foreign keys. A reserved-schema trigger rejects direct device deletion
while routing entries still reference that device, including when foreign keys
are disabled. The primitive requires foreign keys enabled for the legacy cascade.
It processes at most 1,024 candidate batches; corruption, work-budget exhaustion or
any later
SQL failure requires the owner to roll back the entire transaction. Success
must not be acknowledged until the owner commits. No new index, UI deletion
action or live runtime wiring is added. The guard is part of the reserved schema.

Tests compare deletion with legacy cascading links across scopes, retain exact
original observations/claims and unrelated-device links, and verify observation
pruning before deletion, rebuilt bounds/lookups/routing, repeat deletion and
reopen durability. Injected late deletion failure, corruption in a later batch
and exhausted work budgets verify rollback after earlier rewrites. Other checks
cover indexed selection, foreign-key prerequisites, cancellation, closed
transactions and visibility before owner commit.

Explicit claim deletion, global cross-batch/later-legacy-writer
uniqueness, lifecycle/quota integration, migration/rollback and full-controller
resource verification remain required.


## Transactional scoped observation deletion

`DeleteEvidenceBatchObservation` deletes one original observation within an
explicit scope in the caller's exclusively owned transaction. It handles legacy
rows or a batch lookup and refuses an ambiguous original ID present in both
formats. Missing or out-of-scope observations return not found without mutation.
Expired but unpruned originals can still be deleted. The primitive requires
foreign keys enabled and a reserved schema; the caller must roll back on error
and commit before acknowledging success.

Legacy deletion uses SQLite's existing `ON DELETE SET NULL` references. Batch
deletion validates the selected complete batch, routing, lookups and bounds,
removes only the selected observation and its expiry, and clears source/evidence
observation references in its claims and links. It preserves every claim/link ID,
value, time, expiry and other field. It does not prune independently expired
claims or links. An observation-only record is removed when it has no remaining
evidence; an empty batch and its unused routing dictionary are removed.

The existing packed observation lookup selects at most one batch. The shared
bounded codec and transactional rewrite path rebuild slots, bounds and routing,
including original ID expansion and repartition if needed. Unrelated observations
remain exact and readable. No new schema, index, UI action or live runtime wiring
is introduced.

Tests compare legacy reference clearing and exact retained batch evidence,
including expired originals, other records, scope isolation, empty batches,
lookup remapping and reopen. They cover corrupt payload/source/bounds/slots,
duplicate cross-format IDs, a late lookup-insertion failure with rollback,
foreign-key prerequisites, cancellation and owner-only commit. Explicit claim
deletion, global uniqueness, lifecycle/quota integration, migration/rollback and
the unchanged full controller resource measurement remain required.

## Owned mixed-format retention and audit

`Store.PruneEvidenceBatchExpired` owns one mixed-format retention pass on a
separate pinned writer. The connection uses the same foreign keys, FULL
synchronous writes, DELETE journal, disabled trusted schema, busy timeout and
configured page quota as batch ingestion. The two paths share connection setup;
neither creates a missing database or installs the reserved schema. Each
maintenance call closes its private writer and leaves the parent Store open.

One transaction prunes the bounded legacy tables, prunes and validates up to
100 selected batches, and inserts a `retention-expired` storage event. The caller
sets separate limits of 1–10,000 rows per legacy table and 1–100 batches. Original
observation and claim expiry remain independent. Rewrites preserve retained
fields, clear expired observation references, cascade expired claims to links,
and rebuild routing, lookup slots and time bounds. Any staging, audit or commit
failure returns no result; transaction rollback preserves both evidence and audit.
A no-op pass produces no new event. Successful counts are returned after commit.

The result and audit `expired_rows`/`total` fields count expired observations and
claims across both formats plus the other expired legacy rows. As in legacy
retention, they exclude cascaded links and internal index/batch rows. Separate
`batch_observations`, `batch_claims` and `batch_links` fields describe the batch
portion; they must not be added again to `total`. `batches_processed` counts
visited batches. Audit expiry uses the configured audit policy at write time.

Regression coverage verifies exact committed audit counts and reopen, independent
expiry, no-op behavior, separate work limits, missing schema, cancellation,
legacy/batch rollback after audit or rewrite failure, real SQLite quota failure
and recovery, and a held-reader commit failure followed by successful retry.
The existing live `Store.PruneExpired` behavior is unchanged. Scheduling, proactive
pressure policy, global identity constraints, explicit claim deletion,
migration/rollback and full-controller measurement remain activation gates; this
owner is not wired into live maintenance and adds no new index or schema.
