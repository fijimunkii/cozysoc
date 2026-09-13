# Device Watch batch adapter footprint

Related: #150. This workload measures the reserved SQL batch adapter and storage
codec with real Device Watch evidence, device rows and coverage samples. It is a
component measurement before identity-query integration. Live persistence and
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
reconciler constructs claims, links and devices, using a stable in-memory MAC
continuity fixture in place of production identity queries. Devices and coverage
use the existing `Store`; complete observation/claim/link bundles use the actual
reserved adapter, committing each record and its lookup in one transaction.
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
a digest of all original evidence and payload bytes. It verifies all 144,100
observations (including warm-up), 288,200 claims, 288,200 links, 100 devices and
1,441 coverage samples. Page attribution includes every table, index and free
page and must reconcile with the complete logical file size.

## Interpretation

Growth is the final SQLite file size minus its size after warm-up. A completed
test means the evidence and measurement checks passed; the report independently
compares growth with the unchanged 25 MiB/day discovery target. Partial or failed
runs do not establish a daily result.

Included costs are the production codec, independent stored expiry metadata,
batch/source/lookup tables and indexes, device and coverage persistence, and
reserved-schema overhead. Missing costs include production identity/history query
indexes, ingestion queue integration, controller lifecycle, retention audit and
quota recovery integration, and migration/rollback. The full unchanged controller
workload must be repeated after those are implemented. Temporary journal peaks,
filesystem metadata, logs and the UI are outside this logical-file measurement.

The accelerated run is storage-volume evidence, not a 24-hour soak or a CPU/RAM,
latency, scheduling or hardware-support result. Record exact source, hardware,
OS, filesystem and toolchain with published results. Export only sanitized
counts, digests and schema allocation; never a raw database, hardware serial,
hostname or household inventory. Do not relax the target or reference workload.

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
| Unchanged daily target | 25 MiB |
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

The replay and batch/slot indexes together add **10.551 MiB**. This is the next
measured optimization target: reduce duplicated lookup storage while preserving
source-key idempotency, exact record identification, transactional pruning and
referential integrity. The result does not justify adding full per-record
identity indexes without another footprint check. The production 474.50 MiB
baseline remains the live result, and #150 remains open until the integrated
controller meets the unchanged workload and budget requirements.

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
| Excess over 25 MiB | 9.844 MiB | **3.508 MiB** |

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

The adapter still exceeds the target before identity-query integration. The
observation lookup and batch/slot index together still grow by 10.023 MiB. Further
reduction must preserve bounded queries, exact identity, foreign keys and pruning;
this result does not authorize removing those guarantees or increasing the budget.
Live controller storage is unchanged and #150 remains open.
