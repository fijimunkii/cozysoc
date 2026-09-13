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
- `below-target-component-only`: this component workload is below that envelope;
  this is not a whole-controller budget pass.
- `not-evaluated-short-run`: a fixture smoke check, not daily evidence.

Record the exact source commit, hardware model, OS version, filesystem and command
with any published result. Never copy a hardware serial number, hostname, real
interface/address inventory, raw database or household traffic into evidence.
Do not silently increase the budget or lower the workload to turn a result green.
Named-hardware CPU/RAM calibration, overload recovery and the at-least-24-hour
pre-beta sustained run remain separate release gates.
