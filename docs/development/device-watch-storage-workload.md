# Device Watch storage-growth workload

Related issues: #29 and #11. This workload measures the existing Device Watch
collector, ingestion, temporal identity reconciliation and SQLite persistence path
using 100 stable synthetic IPv4 neighbors. It sends no traffic, reads no real
neighbor cache, and does not enroll or change a real network.

## Running it

The small fixture check runs in ordinary Go CI. The full measurement is opt-in:

```sh
TMPDIR=/private/tmp COZYSOC_STORAGE_WORKLOAD=1 go test \
  ./internal/controller/devicewatch \
  -run '^TestDeviceWatchDailyStorageWorkload$' -count=1 -timeout=35m -v
```

Use a suitable temporary directory on Linux. The workload has a thirty-minute
internal deadline and the unchanged default 1 GiB managed-data quota. It owns a
private test directory and removes it after closing ingestion and storage. Do not
run it against product state. An unsuccessful or partial run is not a completed
daily measurement.

## Workload and measurement boundary

One synthetic scope and source feed the production collector through fixture
interface/cache readers. The production storage sink and reconciler persist all
records through the real SQLite implementation and default ingestion queue.
A warm-up collection establishes 100 represented devices before measuring growth.
The next 1,440 collections use successive one-minute source timestamps, matching
the current runtime's default cadence. Each collection presents the same 100
locally administered synthetic MAC addresses and private IPv4 addresses.

The report verifies all 144,000 new observations, all expected ingestion receipts,
no drops or failures, 100 final devices, and retained observation counts after
closing and reopening SQLite. Coverage samples are written on every collection.
The schema, indexes, quota, rollback journal, synchronization and retention settings
are unchanged. All source times are in the past. The shortest applicable retention
is seven days, so expiry does not remove workload data during this first simulated
day; the experiment does not simulate retention recovery or longer-term pruning.

`database_growth_bytes` is the difference in logical SQLite database file length
before the measured collections and after closing storage. It includes persisted
indexes and allocated database pages. It excludes temporary rollback-journal peaks,
filesystem metadata, controller logs, the desktop UI and optional engines. It is
not a per-day extrapolation from a short sample: the full day's collection count
is executed. A short smoke run explicitly reports no daily target comparison.

This is accelerated **storage-volume evidence**, not 24 hours of wall-clock
operation. The runtime scheduler, real OS cache readers, controller/API loops,
service manager and UI are not running. Wall elapsed time is recorded only for
reproducibility; it does not establish CPU, resident-memory, latency or sustained
operation budgets. A result below 25 MiB would not prove that the whole application
meets its storage target. A component result above it establishes that this
workload already exceeds the application's 25 MiB/day architecture envelope.

## Interpreting output

The final `storage-workload-report` JSON identifies the workload version, platform,
Go toolchain, simulated duration, represented-device and observation counts, before/
after database lengths, growth, actual elapsed time and the architecture target.
`target_comparison` is separate from whether the Go measurement test completed:

- `exceeded`: the full workload's growth exceeds 25 MiB.
- `within-target-component-only`: this component workload is at or below that envelope;
  this is not a whole-controller budget pass.
- `not-evaluated-short-run`: a fixture smoke check, not daily evidence.

Record the exact source commit, hardware model, OS version, filesystem and command
with any published result. Never copy a hardware serial number, hostname, real
interface/address inventory, raw database or household traffic into evidence.
Do not silently increase the budget or lower the workload to turn a result green.
Named-hardware CPU/RAM calibration, overload recovery and the at-least-24-hour
pre-beta sustained run remain separate release gates.

## Recorded baseline: September 12, 2026

The [sanitized machine-readable result](evidence/device-watch-storage-2026-09-12.json)
records a completed 1,440-collection run at source commit
`3f6783534f7f41f42e452bffb287cb3a1a346ef7`, with unchanged production code
from `d1c57f177739da5679e779c3f65f1e15f03b6de6`. Hardware was an Apple M4
Mac16,13 with 10 logical CPUs and 24 GiB RAM, running macOS 26.6.2 on APFS
with Go 1.27.1.

| Measurement | Result |
| --- | ---: |
| Database after warm-up | 548,864 bytes |
| Database after measured collections | 498,356,224 bytes |
| Growth | 497,807,360 bytes (474.746 MiB) |
| Architecture target | 26,214,400 bytes (25 MiB) |
| Target comparison | Exceeded by a factor of 18.99 |
| New observations / final devices | 144,000 / 100 |
| Measured wall elapsed time | 1,375,135 ms |

All collection, ingestion, final-device and reopen checks passed. The storage
budget **failed**. [Issue #150](https://github.com/fijimunkii/cozysoc/issues/150)
tracks attribution and reduction without weakening evidence or silently changing
the reference workload. This result does not complete #29 or the resource profile.


## Page attribution (report schema 2)

The workload now records `allocation_before` and `allocation_after` using the
production SQLite driver's `dbstat` table through a separate read-only connection.
Snapshots occur after the warm-up collection and after closing the writer. They
export schema object names and aggregate sizes only, never stored evidence rows.

Each object records its owning table, table/index kind, page count, allocated
bytes, payload bytes and unused bytes. Automatic primary-key and unique indexes
are included. Payload is SQLite record payload, including stored index keys; it
is not a measure of observation JSON alone. Unused bytes are space inside allocated
pages, not removable whole pages. Page headers and other structural bytes account
for the remaining difference. Free-list pages are counted separately.

For both snapshots, the harness requires object pages plus free-list pages to equal
the database page count, and page count times page size to equal the measured file
length. Grouping object-byte differences by owning table separates each table's
records from its indexes without attributing free pages to live evidence. This
accounting measures allocation, not hypothetical savings from deleting an index
or changing representation. It does not run VACUUM or alter persistence settings.

## Recorded attribution: September 13, 2026

The [schema-2 result](evidence/device-watch-storage-attribution-2026-09-13.json)
records the same full workload at source
`c3e72e364339866f3a79f1f33e508a775db2db54`, with production code unchanged
from `5b37ab199e2d89d5bee4c1db9ecb5321cb9313d2`, on the same named hardware
and software settings as the baseline. All 144,000 new observations, 100 final
devices, ingestion, page accounting and reopen checks passed. Growth was
497,545,216 bytes (**474.496 MiB**), still **18.98×** the target. The earlier
474.746 MiB baseline remains a separate run; this is not a production reduction.

The following values are after-minus-before allocated bytes, converted to MiB:

| Owning table | Table pages (MiB) | Index pages (MiB) | Total (MiB) |
| --- | ---: | ---: | ---: |
| `device_claim_links` | 75.188 | 99.770 | 174.957 |
| `observations` | 62.656 | 93.156 | 155.812 |
| `identity_claims` | 56.391 | 85.793 | 142.184 |
| `coverage_samples` | 1.129 | 0.414 | 1.543 |
| **Total** | **195.363** | **279.133** | **474.496** |

Free-list growth was 0 bytes; the listed objects account for all file growth.
Indexes account for **58.83%** of the growth. Even the
**195.363 MiB** allocated to table records alone exceeds the 25 MiB envelope.
This rules out index removal alone as a sufficient fix; it does not justify
removing indexes needed by current queries. Device identity links and claims
are the largest combined allocation, so #150 should evaluate a more compact
evidence representation that preserves original timestamps, provenance, identity
ambiguity and query behavior. Any proposed reduction still needs migration and
correctness tests and a completed unchanged-workload rerun. No storage policy
or budget is changed by this measurement.

The [compression feasibility experiment](device-watch-compression-feasibility.md)
compares bounded lossless batch encodings. Its payload-only results do not replace
the persisted measurements above.
