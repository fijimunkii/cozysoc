# Coverage and sensor-health read model

Issue #12 turns retained evidence into user-facing coverage state without inventing a global protection score.

The first concrete read model is Device Watch. It combines retained normalized `CoverageSample` evidence with current sensor, ingestion, controller-database, and host-volume operational health. Security findings remain a separate model: an unhealthy sensor is not itself a security incident, and an absence of findings is never proof that the network is safe.

## Device Watch coverage API

The authenticated read-only method is:

```text
device-watch.coverage
```

CLI:

```bash
cozysoc-controller device-watch-coverage --state-dir PATH
```

The method is parameterless. The caller cannot select another scope, sensor, source, historical time, or arbitrary stored evidence. The controller reports only the Device Watch scope selected by current durable intent.

If Device Watch is not enabled/configured, the response is `unconfigured` and points the user back to network enrollment and explicit enablement.

The response keeps the existing Device-Watch-specific fields for compatibility and now also includes a nested capability-independent `coverage` report. The outer `as_of` remains the evaluation time for this endpoint; each shared observation point carries its own evidence window. The legacy aggregate `state`, `reason`, and `next_step` are copied from the validated shared report so the two views cannot drift.

## Shared observation-point contract

The reusable contract is defined in [`coverage-contract.md`](coverage-contract.md). It separates:

- capability state from individual observation points;
- configured scope from currently verified scope and expected-but-unverified scope;
- network/interface/VLAN/device/address-family/wireless band/channel dimensions;
- expected and observed sources;
- directions actually observed from directions that remain gaps;
- evidence windows from observation cadence; and
- operational state from explicit known exclusions.

It has no percentage field and no implicit denominator for unknown devices, networks, VLANs, or wireless channels.

Device Watch is the only live producer today. Deterministic DNS, gateway-packet, wireless-hopping, and permission-required fixtures prove only that the contract can represent those #12 requirements. They are **not** product-support claims for DNS Protection, Traffic Watch, router visibility, or Wireless Watch.

Device Watch itself declares no observed traffic directions. Its shared `no-traffic-monitoring` gap explicitly carries ingress, egress, and east-west as directions that ARP/NDP neighbor evidence does not establish.

## Aggregate coverage states

The detail read model uses the same current evidence and operational checks that feed lifecycle verification:

- `unverified` — required current evidence or first-collection validation is not available yet;
- `active-limited` — current passive neighbor-cache evidence exists, both expected ARP/NDP sources are available, and the sensor/ingestion/storage path is currently healthy, while Device Watch still has its declared observation-point limitations;
- `degraded` — a source, sensor collection, measured ingestion-lag/write path, database quota, or host-volume capacity check is currently unhealthy, or retained coverage evidence is invalid/untrustworthy;
- `stale` — the latest successful collection/evidence is older than the bounded freshness window; and
- `disconnected` — the configured Device Watch runtime or ingestion path is no longer running.

`active-limited` is deliberately not named `healthy`, `protected`, or `full`. Even with both ARP and NDP sources current, Device Watch remains a local passive-neighbor capability.

A current sample with one working source and one unavailable source is **degraded**, not verified merely because some evidence still arrives. The working source remains visible as `current` in the detail response so partial usefulness is preserved without hiding the gap.

Operational causes take precedence over an otherwise-fresh evidence sample. For example, if the last sample is still inside its freshness window but the Device Watch runtime has stopped, the aggregate state is `disconnected`, while the old evidence remains visible underneath with its timestamp.

The shared report also preserves the evidence/operation distinction underneath aggregate degradation. Current ARP/NDP evidence can keep its verified address-family dimensions even when a separate ingestion-lag or storage-health failure makes the observation point operationally degraded.

## Source health

The response reports the two currently expected sources independently:

- `arp-cache` / IPv4;
- `ndp-cache` / IPv6.

A source state can be:

- `current` — the latest fresh sample reported that source available;
- `unavailable` — the latest fresh sample reported that source unavailable;
- `stale` — the latest sample is too old to establish current source health;
- `missing` — no coverage sample exists yet;
- `unknown` — evidence exists but cannot be trusted, for example because its schema/status is invalid or its timestamp is implausibly ahead of the controller clock.

`reported` and `available_at_last_sample` are separate. This keeps "we have no current evidence" distinct from "the last trusted collection explicitly said this source was unavailable."

In the shared contract, legacy Device Watch `missing` maps to `expected-unverified`. The shared vocabulary can also represent `permission-required`, but Cozy SOC does **not** currently relabel neighbor-source `unavailable` that way: the passive source does not yet provide enough evidence to distinguish permission denial from a missing/failed system source.

## Sensor operational health

The configured Device Watch runtime is evaluated independently from the evidence payload:

- `current` — the runtime is running and its last successful collection is current;
- `starting` — the runtime is running but has not completed its first successful collection;
- `degraded` — the most recent collection attempt failed or the sensor timestamp is not trustworthy;
- `stale` — the runtime is still running but no successful collection has completed inside the freshness window;
- `disconnected` — the configured runtime is no longer running; and
- `unavailable` — the platform/runtime cannot provide Device Watch operational state.

The response includes the last attempt, last success, and a coarse bounded error class. Those fields describe collection health, not attack evidence.

## Ingestion health, measured lag, and write recovery

The bounded ingestion queue separates utilization from measured health.

Queue depth at or above 75% of capacity sets `queue_pressure=true`, but **does not by itself degrade coverage**. It is useful capacity telemetry: a queue may be busy while every accepted record is still reaching durable storage promptly. Actual current ingestion states are:

- `current` — no current measured lag, backpressure, or write failure is established;
- `lagging` — accepted evidence has exceeded the bounded durable-latency rule below;
- `backpressure` — the current saturation episode has rejected/dropped evidence;
- `write-failed` — a generic current storage write has failed;
- `storage-full` — SQLite returned typed `SQLITE_FULL` for the current write-failure episode; and
- `closing` / `closed` — the ingestion path is shutting down or disconnected.

For every successfully accepted queue record, the producer and worker synchronize on the actual channel-acceptance boundary. The controller then measures:

- queue wait: accepted → processing start;
- processing duration: processing start → storage operation completion; and
- durable latency: accepted → successful durable completion.

The initial lag threshold is **5 seconds**. This is a conservative implementation warning anchored to half of the existing 10-second per-storage-operation timeout; it is not a measured hardware SLO and #29 remains responsible for named-workload performance evidence.

Ingestion becomes `lagging` when either:

- the oldest accepted pending record has waited at least 5 seconds without completing; or
- three consecutive recent successful durable completions each took at least 5 seconds.

Completed-latency evidence is considered current for one minute. If the pipeline becomes quiet with no pending work, old slow samples age to `idle` rather than keeping coverage degraded forever. One later fast successful completion resets the slow streak. Failed writes do not count as successful durable-latency samples; their explicit failure state takes precedence instead.

The API reports `latency_state`, the threshold, pending count/oldest age, last durable latency, last queue wait, last processing duration, last completion time, and current slow streak in milliseconds where applicable. These values are operational measurements, not security findings or protection percentages.

The SQLite-full classifier uses SQLite's result code rather than matching a human-readable error string. The API exposes only a bounded failure class such as `sqlite-full`; raw driver/database error text is not promoted into user-facing coverage state.

A `storage-full` episode remains degraded after capacity has been freed until a later storage operation succeeds. This preserves the distinction between **capacity appears recovered** and **the write path has actually demonstrated recovery**. Cumulative dropped and failed totals remain visible after recovery without permanently poisoning current health.

## Database quota versus host-volume capacity

The existing `storage-health` signal keeps two capacity domains separate.

### Controller database quota

The SQLite store has a configured `max_page_count` quota. The response reports:

- allocated database bytes;
- actively used page bytes;
- reusable free-list page bytes; and
- the effective configured maximum bytes.

Quota pressure begins when **used** pages reach 90% of that known database quota; reaching the effective quota is a degraded state. Reusable free-list pages count as available headroom, so retention pruning does not falsely leave the database "almost full" simply because the file has not shrunk on disk.

### Host-volume capacity

On Darwin and Linux the controller also reads filesystem capacity for the volume containing the Cozy SOC state directory. It reports whether that measurement is supported, total bytes, bytes available to the process, and the explicit warning threshold.

Filesystem states are:

- `current` — more than 128 MiB is currently available;
- `pressure` — available bytes are nonzero but at or below 128 MiB;
- `full` — the operating system reports zero bytes available to this process; and
- `unavailable` — current capacity could not be established.

The 128 MiB value is a bounded operational-headroom warning, not a prediction that the next write will fail. `full` is not guessed from database size or a historical failure; it requires current filesystem evidence reporting zero available bytes.

Database quota and host-volume state are both returned so the UI can explain the distinction. Examples:

- DB quota reached + host volume current → `storage-quota-reached`;
- host volume full + DB quota current → `storage-filesystem-full`;
- typed `SQLITE_FULL` + current capacity no longer limiting → `ingestion-sqlite-full` until a successful write proves recovery.

If filesystem capacity introspection is unsupported, that fact is exposed without degrading the capability by itself. If the platform is expected to support it but the measurement currently fails, storage health becomes `filesystem-unknown` rather than guessing either current or full.

## Evidence validation

The Device Watch producer strictly validates its source-specific coverage evidence schema before presenting it, using it for lifecycle verification, or projecting it into the shared contract.

It requires:

- the expected schema version;
- one enrolled interface name;
- exactly one ARP and one NDP source entry;
- internally consistent non-negative observation counters;
- a status consistent with source availability; and
- `whole_network_traffic_visible=false`.

Malformed or contradictory evidence fails closed as degraded instead of being guessed healthy.

Stored free-form limitation strings are not copied into the frontend contract. The controller emits curated blind spots and next steps so a corrupted or hostile stored string cannot become authoritative UI guidance.

The shared contract performs an additional structural validation over observation-point identity/state, scope dimensions, source states, directions, evidence windows, cadence, and gaps. It does not replace source-specific validation: future producers must first prove the meaning of their own evidence and then map it into the common vocabulary.

## Curated Device Watch blind spots

Every configured Device Watch report preserves these limitations:

1. **Host neighbor cache only** — only peers recently resolved by the enrolled Mac may appear. An unseen device is unknown, not proved absent or safe.
2. **Isolated segments are not observed** — client isolation and other VLANs/subnets may hide devices from this observation point.
3. **No traffic monitoring** — ARP/NDP evidence does not reveal other devices' uploads, east-west flows, packet contents, or application behavior.

Each gap includes one concrete next step. These are guidance, not automated response actions.

## Relationship to capability verification

Device Watch declares five independent verification signals:

1. `network-scope-enrolled`;
2. `observation-freshness`;
3. `sensor-operational`;
4. `ingestion-health`; and
5. `storage-health`.

All must be fresh for the capability to be `verified`. This prevents a fresh historical sample from keeping the capability green after its sensor disconnects, accepted evidence begins lagging, or its write/capacity path becomes unhealthy.

The lifecycle state and `device-watch.coverage` share the same operational evaluators. The shared coverage report is then projected from that same result. The UI therefore cannot say that a sensor is disconnected, stale, measurably lagging, backpressured, at database quota, or out of host-volume capacity while `capabilities.list` independently calls the same capability verified. High queue utilization alone remains visible as a warning without failing `ingestion-health` when measured durable latency is still current.

A fresh sample may still contain zero neighbors. The sample itself is heartbeat/validation evidence that collection ran, so a quiet network is not automatically a source failure.

## Still outside this slice

This does not complete #12. The shared contract can now represent DNS-client scope, gateway/east-west direction gaps, wireless hopping/dwell/encryption limits, and explicit permission-required state in deterministic fixtures, but real product producers are still required before any of those become support claims.

Remaining work includes real DNS/router/traffic/wireless adapters and source-specific validation, permission evidence where a producer can actually prove it, multi-observation-point aggregation once a real capability has more than one point, a generic frontend renderer, named-workload calibration of operational thresholds, and real low-disk/full-volume/hardware evidence under #29.
