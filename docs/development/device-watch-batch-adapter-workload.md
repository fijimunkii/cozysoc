# Device Watch batch adapter footprint

Related: #150. This workload measures the reserved SQL batch adapter and storage
codec with real Device Watch evidence, device rows and coverage samples. It is a
component measurement with reconciliation identity queries. Live persistence and
migrations do not use the adapter, and this workload cannot establish the full
controller's storage budget.

## Running and verifying

```sh
TMPDIR=/private/tmp COZYSOC_BATCH_ADAPTER_WORKLOAD=1 go test \
  ./internal/controller/storage \
  -run '^TestEvidenceBatchAdapterDailyFootprint$' -count=1 -timeout=35m -v
```

Use a suitable private temporary directory on Linux. Ordinary CI runs a
three-collection smoke check, which makes no daily target comparison. The full
run executes 1,440 collections of 100 stable synthetic neighbors, after one
100-device warm-up collection. Source timestamps advance by one minute; the
workload does not read real caches or send traffic. It has a thirty-minute
internal deadline and creates and removes its own private database.

The real collector constructs observations and coverage samples. The real
reconciliation planner constructs claims, links and devices using the combined
legacy/batch identity reader in the same transaction that writes each bundle, its
lookup and any new device. Coverage uses the existing `Store`. This fixture uses
unique source keys; production replay-before-planning orchestration remains an
integration requirement.
A test-only bridge avoids an import cycle; it does not expose a production API or
activate the schema in `Store.Open`.

Each observation and claim receives its own expiry from the current write-time
clock plus the unchanged retention class duration, as in live storage. This
includes nanosecond precision and preserves each stored value; it does not use a
single fixed expiry for a collection or recompute expiry from a past source time.
The codec serializes these independent values, so their footprint is part of the
measurement. The earlier standalone prototype's fixed expiry is not equivalent.

The dedicated writer verifies DELETE journaling, FULL synchronization, foreign
keys, 4 KiB pages and the default 1 GiB page quota. The measurement does not vacuum,
reduce retention, weaken durability or drop observations. After closing both
writers, it reopens read-only, verifies every batch and lookup slot, and compares
a digest of all original evidence and payload bytes. Grouping changes physical
iteration order, so current reports use `sha256-sorted-record-sha256-v1`: reject
duplicate observation IDs, hash each complete original record and raw payload,
then hash those fixed-length digests in observation-ID order. Historical reports
retain their earlier insertion-order digest and cannot be compared to this format. It verifies all 144,100
observations (including warm-up), 288,200 claims, 288,200 links, 100 devices and
1,441 coverage samples. Page attribution includes every table, index and free
page and must reconcile with the complete logical file size.

## Interpretation

Growth is the final SQLite file size minus its size after warm-up. A completed
test means the evidence and measurement checks passed; the report independently
compares growth with the current 30 MiB/day discovery target. Partial or failed
runs do not establish a daily result.

Included costs are the production codec, independent stored expiry metadata,
batch/source/lookup tables and indexes, device and coverage persistence, and
reserved-schema overhead and reconciliation identity routing/queries. Missing costs
include history/detail/activity query indexes, ingestion queue integration, controller lifecycle, retention audit and
quota recovery integration, and migration/rollback. The full unchanged controller
workload must be repeated after those are implemented. Temporary journal peaks,
filesystem metadata, logs and the UI are outside this logical-file measurement.

The accelerated run is storage-volume evidence, not a 24-hour soak or a CPU/RAM,
latency, scheduling or hardware-support result. Record exact source, hardware,
OS, filesystem and toolchain with published results. Export only sanitized
counts, digests and schema allocation; never a raw database, hardware serial,
hostname or household inventory. Budget revisions require a recorded decision; keep the reference workload unchanged.

The recorded results below precede identity routing and used an in-memory continuity
fixture. They are historical component measurements; remeasure the current adapter
before using them as its footprint. Historical targets and digests are unchanged.

## Recorded result: September 13, 2026

The [sanitized result](evidence/device-watch-batch-adapter-2026-09-13.json) records
source `944cc17486c64bb6a1081291e96b2ee9983893ab`, using the reserved adapter from
`41ed447a058ecdae2a99c922a275c404da5dda47`. Hardware was an Apple M4 Mac16,13,
10 logical CPUs and 24 GiB RAM, with macOS 26.6.2, APFS, Go 1.27.1 and SQLite
3.53.4. All 1,440 measured collections completed, and all evidence, lookup,
device/coverage count, reopen digest and file/page-accounting checks passed.

| Measurement | Result |
| --- | ---: |
| Database after warm-up | 290,816 bytes |
| Final database | 36,827,136 bytes |
| Daily growth | **36,536,320 bytes / 34.844 MiB** |
| Daily target at measurement | 25 MiB |
| Excess before identity-query integration | **9.844 MiB** |
| Reopened observations, including warm-up | 144,100 |
| Reopened claims / links | 288,200 / 288,200 |
| Persisted devices / coverage samples | 100 / 1,441 |
| Measured write and verification elapsed time | 487,144 ms |

The digest of all reopened evidence and original payload bytes was
`7140a8916dfa12bd91141016bc07543d6a54e0f480ca925cc9f32505df21c2db`.
Write-time expiry values make that digest specific to this run; subsequent runs
must verify their own original-versus-reopened digest.

| Growth attribution | Bytes | MiB |
| --- | ---: | ---: |
| Compressed batch table | 17,711,104 | 16.891 |
| Observation lookup table | 6,082,560 | 5.801 |
| Source-key replay index | 6,635,520 | 6.328 |
| Unique batch/slot index | 4,427,776 | 4.223 |
| Coverage table and indexes | 1,622,016 | 1.547 |
| Batch expiry/source indexes | 53,248 | 0.051 |
| Free-page growth | 4,096 | 0.004 |
| **Total** | **36,536,320** | **34.844** |

The replay and batch/slot indexes together added **10.551 MiB**, motivating the
sparse replay measurement below while preserving
source-key idempotency, exact record identification, transactional pruning and
referential integrity. The result does not justify adding full per-record
identity indexes without another footprint check. The production 474.50 MiB
baseline remains the live result, and #150 remains open until the integrated
controller meets the unchanged workload and current budget requirements.

## Sparse replay index: September 13, 2026

The [sparse-index result](evidence/device-watch-batch-adapter-sparse-replay-2026-09-13.json)
records source `fac2789d94c86f9fb643c203a4ef60a092a2ccaf` with the same workload,
hardware, toolchain, durability and retention settings. Matching observation and
source keys reuse the primary lookup; only differing source keys occupy the
partial replay index. Insert/update triggers preserve uniqueness across both
representations. No observation, claim, link or lookup was omitted.

| Measurement | Baseline | Sparse replay index |
| --- | ---: | ---: |
| Daily growth | 34.844 MiB | **28.508 MiB** |
| Replay-index growth | 6.328 MiB | **0 MiB** |
| Final file size | 36,827,136 bytes | 30,183,424 bytes |
| Excess over then-current 25 MiB | 9.844 MiB | **3.508 MiB** |

Total growth fell by **6,643,712 bytes (6.336 MiB)**. Of that difference,
6,635,520 bytes are removed replay-index growth and 8,192 bytes are the difference
in free-page reuse. The partial replay index retains its empty 4 KiB root page in
this matching-key workload. Batch, observation-lookup, batch/slot-index and coverage
allocations are unchanged. Arbitrary source keys still use the partial index.

All 1,440 collections and full reopen verification passed: 144,100 observations,
288,200 claims, 288,200 links, 100 devices and 1,441 coverage samples. The reopened
evidence digest was
`68a256b7f49f93bc04a08619cbf81fb907a2a2ceea58405468f993534f639d17`.
Write and verification elapsed time was 484,099 ms; this is not a CPU/RAM or
latency-budget result. Each run preserves its own independently assigned expiry
values, so the two runs are not expected to share an evidence digest.

Against the [revised 30 MiB/day target](../architecture/resource-budgets.md#discovery-growth-budget-decision),
the measured 29,892,608-byte growth fits with **1,564,672 bytes (1.492 MiB)**
remaining. Historical tables and JSON retain their original 25 MiB comparisons.
Further compaction solely to meet 25 MiB is no longer required. The next step is
production query, lifecycle and migration integration, preserving bounded queries,
exact identity, foreign keys and pruning, followed by the full controller workload.
Live controller storage is unchanged and #150 remains open.

## Identity routing and real reconciliation queries: September 13, 2026

The [identity-query result](evidence/device-watch-batch-identity-2026-09-13.json)
records source `1ddb3ccfd0ed8e182df2b21a1b1cc2eeb822d64a` on the same Apple M4
Mac16,13, macOS 26.6.2, APFS, Go 1.27.1 and SQLite 3.53.4 configuration. The
1,440 measured collections now use the actual mixed legacy/batch identity reader
and reconciliation planner in each bundle's transaction. There is no in-memory
continuity substitute. All evidence, lookup, routing, bounds and reopen checks
passed, with exactly 100 persisted devices.

| Measurement | Result |
| --- | ---: |
| Database after warm-up | 462,848 bytes |
| Final database | 30,527,488 bytes |
| Daily growth | **30,064,640 bytes / 28.672 MiB** |
| Current daily target | 31,457,280 bytes / 30 MiB |
| Remaining margin | **1,392,640 bytes / 1.328 MiB** |
| Reopened observations, including warm-up | 144,100 |
| Reopened claims / links | 288,200 / 288,200 |
| Persisted devices / coverage samples | 100 / 1,441 |
| Write and verification elapsed time | 777,724 ms |

Growth includes 17.121 MiB of batch pages, 5.844 MiB of observation lookup,
4.094 MiB of batch/slot uniqueness, 1.543 MiB of coverage and 0.070 MiB of batch
indexes. The identity dictionaries and their indexes occupy 98,304 bytes already
established by the unchanged warm-up; they add no growth for the stable keys in
this workload. Those initial pages are present in both allocation snapshots.
Changing identity keys can add dictionaries and is covered by correctness tests,
not by this stable-workload growth result.

The `sha256-sorted-record-sha256-v1` digest is
`bf2fae4d2cb16e3429444a28def3c45b663b95332b9119078475d539a640ae51`.
It covers every original record and payload, rejecting duplicate observation IDs.
It is not comparable with earlier insertion-order digests or runs with different
independently assigned expiry values.

This component now fits the revised budget with reconciliation identity queries.
It does not activate live batch storage or establish a whole-controller pass.
History/detail/activity readers, replay-before-planning ingestion, complete
foreign-key and quota/lifecycle behavior, migration/rollback and the full controller
workload remain required. The live 474.50 MiB result and separate CPU/RAM and
sustained-run gates remain unchanged.
