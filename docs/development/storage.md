# Normalized local storage v1

Issue #10 introduces the controller-owned persistence and ingestion contract for normalized Cozy SOC evidence.

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

## Retention and quota

The store records an explicit expiry timestamp on evidence-bearing rows and has four internal retention classes. The initial durations are implementation defaults, not a permanent product promise:

- `ephemeral`: 24 hours;
- `short`: 7 days;
- `standard`: 30 days; and
- `audit`: 180 days.

#30 will expose and refine user-facing retention controls. The current store hard-limits the main database with SQLite `max_page_count`; the default is the architecture target of 1 GiB. Transient filesystem overhead and sustained-growth behavior still require #29 measurement.

Retention deletion is bounded per call. When rows expire, the controller writes a `retention-expired` storage event with per-table counts rather than allowing old evidence to disappear with no operational trace.

## Privacy

Normalized JSON envelopes are capped at 64 KiB. The v1 schema has no full-packet/PCAP column and the product contract forbids adapters from storing raw packet payloads through generic observation metadata. Future diagnostic packet capture, if added, must be a separately consented, bounded path under #30 rather than an accidental use of the normalized event store.

Secret bytes do not belong in this database. Credential-bearing capability fields remain opaque references to the secret-store boundary.

## What remains in #10

This slice still does not close #10. Remaining work includes:

- merge/split correction operations with audit records;
- overlap/double-count representation for multiple sensors;
- richer migration fixtures;
- low-disk/`SQLITE_FULL` recovery; and
- controller/API wiring once #11 begins producing real Device Watch observations.
