# Device Watch persistent batch prototype

Related: #150, following [compression feasibility](device-watch-compression-feasibility.md).
This test-only prototype stores real SQLite batches in a private, owned test
directory. It is not connected to the controller, does not open product state,
and does not migrate the production database. The production storage-budget
failure remains open.

## Write and read behavior

Every `Put` uses one transaction to rewrite the current compressed batch and insert
its observation lookup entry. It returns success only after COMMIT. A partially
filled batch is durable immediately; records are not held in memory until the next
collection. Each batch holds at most 100 records and has independent gzip data.
An append that exceeds the byte limit starts a new batch if the individual record
fits. SQLite uses DELETE journaling and FULL synchronization, checked in regression
coverage. No weaker durability setting is used to speed up the experiment.

The lookup table uses the observation ID as a WITHOUT ROWID primary key and stores
batch ID, slot and an explicit observation expiry. Point lookup joins one indexed
entry to one batch in a single SQL statement, then decodes at most 100 records and
1 MiB. It checks that the indexed slot contains the requested original ID and hides
records at or past the supplied expiry. An identical replay with the same expiry
is idempotent; conflicting evidence or expiry is rejected. This is not yet the
production source-key idempotency contract or its independent claim-retention policy.

## Codec boundary

Version 1 envelopes include a per-record choice between verified derived IDs and
full IDs. Noncanonical IDs remain stored in full, including their references.
Decoded records are validated against the domain contracts. Original observation
payload bytes are stored separately as bytes, preserving JSON whitespace as well
as values. Decode does not re-run reconciliation or extend evidence windows.

Encoded and decoded batches are limited to 1 MiB, batches to 100 records, and each
record to eight claims and eight links. Observation payloads are checked against
the domain byte limit before copying. The decoder rejects unsupported versions,
unknown/duplicate fields, noncanonical envelope JSON, extra gzip streams, corrupt
checksums, truncation, excess counts and decoded output over the limit. SQL point
reads guard the BLOB length before returning it to Go. The limits are prototype
choices to be evaluated against all production-supported evidence shapes.

## Regression evidence

Ordinary Go tests cover canonical and noncanonical IDs, exact payload bytes,
codec corruption and size rejection, partial-batch reopen, replay conflicts,
expiry boundaries and batch rollover. An injected lookup-insert error verifies
that the earlier payload update rolls back in the same transaction. A child
process changes a batch and inserts a lookup inside an uncommitted transaction;
the parent terminates it, reopens SQLite and verifies that previously acknowledged
evidence survives and neither uncommitted change remains. This is process-crash
recovery evidence, not a physical power-loss or filesystem-fault claim.

## Full prototype measurement

```sh
TMPDIR=/private/tmp COZYSOC_BATCH_PROTOTYPE=1 go test \
  ./internal/controller/devicewatch \
  -run '^TestPrototypeBatchDailyPersistence$' -count=1 -timeout=35m -v
```

Use an appropriate temporary directory on Linux. The full workload has a
30-minute internal deadline and executes 1,440 collections of 100 synthetic
neighbors. Production observation construction and reconciliation use an in-memory
stable-continuity fixture; production identity queries are absent. Each observation,
its claims and links is committed separately. There is no warm-up collection.

After closing and reopening, the verifier checks every batch and lookup slot and
compares a digest of all 144,000 original evidence records, including raw payload
bytes. It then reconciles SQLite page allocation with the complete prototype file
length. A partial run is not a completed measurement. Verification scans are an
offline test operation; they are not a proposed implementation for history APIs.

The prototype file includes batch and point-lookup storage. It excludes device and
coverage records, source-key indexes, production history/presence/identity queries,
independent claim expiry, pruning, quota recovery and controller overhead. Even a
file below 25 MiB would not establish the whole-controller target. Full production
query integration, retention/quota behavior, transactional migration and rollback,
and the unchanged persisted workload from #151/#152 remain necessary. CPU/RAM and
24-hour sustained-run gates also remain open.
