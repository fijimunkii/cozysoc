# Normalized local storage v1

Issue #10 established the controller-owned persistence and ingestion contract for normalized Cozy SOC evidence. #10 is complete; later coverage and operational-health work builds on that contract without reopening it.

## Boundaries

The database is an implementation detail of the controller. Renderers, engines, integrations, and future sensors do not open the SQLite file directly.

The store uses one dedicated controller connection and rollback journaling. This is a correctness baseline, not a claim that rollback mode will always outperform WAL. #29 owns workload measurements before a journal-mode change.

## Logical model

The v1 schema persists:

- enrolled `NetworkScope` records;
- `Sensor` observation points;
- immutable normalized `Observation` records;
- user-facing `Device` records;
- immutable temporal `IdentityClaim` evidence;
- separate temporal device↔claim links with inferred/user authority;
- `CoverageSample` evidence envelopes;
- `Finding` records plus observation evidence references;
- security-relevant `AuditEvent` records;
- ingestion checkpoints for safe restart/replay; and
- storage/retention events so expired or dropped evidence is observable.

`Activity` remains a query projection over observations and temporal device links. It is not stored as a second copy of the same event stream.

## Observation idempotency

Every normalized observation has both a Cozy SOC ID and a source identity tuple:

`(sensor_id, source_stream, source_key)`

That tuple is unique in SQLite. An adapter should use an upstream event ID when available; otherwise it must derive a stable source key from the source record rather than generating a new random value on every replay.

`source_event_id` is retained separately for upstream provenance. `source_time` may be missing, skewed, or out of order; Cozy SOC also records its own trusted ingestion time. Clock disagreement is evidence, not a reason to reorder or discard the source event.

## Bounded ingestion

Live evidence enters storage through a bounded controller-owned ingestion queue. The default capacity is 256 records and the implementation refuses capacities above 8192.

The normal `Submit...` methods apply backpressure by waiting for queue capacity until the caller context ends. Callers that explicitly choose non-blocking observation submission receive `ErrQueueFull` rather than silent loss.

Every accepted record receives an `IngestionReceipt`. The receipt completes only after the storage operation finishes, so a producer may batch asynchronously and still wait at a durability boundary when needed. Receipt results distinguish a newly inserted observation from a replay that was deduplicated.

Observation checkpoints are advanced only after the observation insert succeeds. If the observation was already present because of replay, the checkpoint is still advanced. Therefore a crash between the original observation write and checkpoint write is recoverable by replay rather than creating duplicate evidence.

The queue publishes bounded in-memory statistics for accepted, processed, deduplicated, rejected, dropped, and failed records. Queue-overflow and storage-write failure episodes use a small reserved internal event lane and become `ingestion-backpressure` or `ingestion-write-failed` storage events. These events never contain the rejected observation payload or a raw driver/database error.

### Measured latency

Each successful channel acceptance has a synchronized acceptance timestamp. The single worker waits for that acceptance marker before it begins processing, so the timing boundary is the real accepted queue entry rather than the caller's submission attempt.

For successfully durable records the controller measures:

- queue wait: accepted → processing start;
- processing duration: processing start → storage completion; and
- durable latency: accepted → successful storage completion.

The initial lag threshold is 5 seconds, anchored conservatively to half of the existing 10-second per-storage-operation timeout. It is an implementation warning threshold, not a measured hardware SLO. #29 remains responsible for named-workload calibration.

Current latency state can be:

- `idle` — no pending work and no recent successful latency sample;
- `current` — recent/pending work remains below the lag rule; or
- `lagging` — the oldest accepted pending item is at least 5 seconds old, or three consecutive recent successful durable completions each took at least 5 seconds.

Successful latency samples remain current for one minute. Quiet pipelines therefore age old measurements to `idle` instead of staying degraded from historical slowness. A later fast successful write resets the slow streak. Failed writes are removed from the pending timing set but do not become successful durable-latency evidence.

Queue utilization remains separate: depth at or above 75% sets a queue-pressure diagnostic, but high utilization alone no longer fails ingestion health. This avoids using queue depth as a proxy for slowness when accepted records are still reaching durable storage promptly.

Current ingestion health states are therefore:

- `current` when no current measured lag, backpressure, or write failure is established;
- `lagging` while the measured durable-latency rule is tripped;
- `backpressure` while the current saturation episode is dropping/rejecting evidence;
- `write-failed` while a generic current storage-write failure is active;
- `storage-full` when the current write failure carries SQLite's typed `SQLITE_FULL` result code; and
- `closing` / `closed` during shutdown or disconnection.

The SQLite-full classifier uses the driver's typed error code rather than matching human-readable database error text. The active failure stores only a bounded class such as `sqlite-full`; raw database errors are not copied into storage events or the coverage API.

A `storage-full` ingestion episode remains degraded until a later storage operation succeeds. Current filesystem or quota capacity may recover first, but Cozy SOC does not claim the write path recovered until a successful write proves it. Cumulative dropped/failed totals remain visible for diagnostics after recovery without permanently poisoning current health.

Shutdown stops new submissions and drains all already-accepted records. A shutdown context may time out, but the ingestor does not silently discard the remaining accepted queue when that happens.

## Bounded queries

Observation history queries are always scoped to one enrolled network. They use keyset pagination over trusted controller ingestion time, default to a 24-hour window, reject windows over 31 days, and cap pages at 200 records.

The query layer filters logically expired evidence even before a retention-prune pass physically removes it. Optional sensor and observation-kind filters remain parameterized SQL values rather than dynamic SQL identifiers.

Device listing is also network-scoped and temporal. A device appears only when retained identity evidence and a device↔claim link are valid at the requested time. Overlapping links are not collapsed: if current evidence genuinely supports two possible devices, both remain visible until later evidence or an audited user correction resolves the ambiguity.

## Temporal identity

Identity is deliberately not represented as `device.mac` or `device.ip`.

An identity claim records that a specific source observed a scoped value at a time. Address-like claim values are canonicalized before persistence. IPv4/IPv6 values are always scoped to the enrolled network, so identical RFC1918 addresses on different networks are unrelated unless other evidence links them.

A device↔claim link is a separate temporal assertion with:

- validity interval;
- confidence;
- `inferred` or `user` authority;
- a reason; and
- optional direct observation evidence.

This makes DHCP reuse and merge/split corrections representable without rewriting the original claim. User labels live on the `Device`, not inside inferred hostname/service claims.

## Retention, database quota, and host-volume capacity

The store records an explicit expiry timestamp on evidence-bearing rows and has four internal retention classes. The initial durations are implementation defaults, not a permanent product promise:

- `ephemeral`: 24 hours;
- `short`: 7 days;
- `standard`: 30 days; and
- `audit`: 180 days.

#30 will expose and refine user-facing retention controls. The current store hard-limits the main database with SQLite `max_page_count`; the default is the architecture target of 1 GiB. Transient filesystem overhead and sustained-growth behavior still require #29 measurement.

Operational database-quota health uses SQLite page accounting rather than raw file size alone. The controller reports allocated database bytes, actively used page bytes, reusable free-list bytes, and the effective `max_page_count` capacity. Pressure begins when used pages reach 90% of that configured database quota; reusable free-list pages count as headroom, so retention pruning can recover capacity even if the database file has not physically shrunk.

Host-volume capacity is a separate signal. On Darwin and Linux the controller reads filesystem statistics for the volume containing the state directory and reports total and **available-to-this-process** bytes. Filesystem states are:

- `current` when available bytes are above the bounded warning threshold;
- `pressure` when available bytes are at or below 128 MiB but still nonzero;
- `full` only when the operating system reports zero available bytes; and
- `unavailable` when capacity cannot be established.

The 128 MiB value is a controller operational-headroom warning, not a claim that the OS will fail the next write at that point. It is also not a protection score. `full` is not inferred from database size: it requires current filesystem evidence reporting zero available bytes.

Database quota and host-volume capacity can therefore disagree legitimately. A database can be at its SQLite quota while the host volume has ample space, or the host volume can be nearly/full while the database remains far below its own quota. When SQLite returns typed `SQLITE_FULL`, the current quota/filesystem evidence is used to explain the likely limiting capacity. If neither current capacity signal is limiting, the ingestion state remains `sqlite-full` until a successful write proves recovery rather than inventing a cause.

On unsupported platforms filesystem capacity is reported as unsupported/unavailable and does not by itself degrade storage verification. On a platform where capacity introspection is expected but cannot currently be read, the filesystem state is `unavailable` and coverage degrades as `filesystem-unknown` rather than guessing current or full.

Retention deletion is bounded per call. When rows expire, the controller writes a `retention-expired` storage event with per-table counts rather than allowing old evidence to disappear with no operational trace.

## Privacy

Normalized JSON envelopes are capped at 64 KiB. The v1 schema has no full-packet/PCAP column and the product contract forbids adapters from storing raw packet payloads through generic observation metadata. Future diagnostic packet capture, if added, must be a separately consented, bounded path under #30 rather than an accidental use of the normalized event store.

Secret bytes do not belong in this database. Credential-bearing capability fields remain opaque references to the secret-store boundary.

## Follow-on work after completed #10

The normalized-storage milestone is complete. Follow-on issues still own product and lab work that builds on it, including:

- auditable merge/split correction UX and broader multi-sensor overlap behavior where later capabilities need it;
- named-workload calibration and overload recovery for the measured latency/resource thresholds;
- real low-disk/full-volume recovery evidence on named filesystems/hardware; and
- broader controller/API projections for future sensors and capabilities.
