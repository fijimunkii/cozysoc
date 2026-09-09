# Coverage and sensor-health read model

Issue #12 turns retained evidence into user-facing coverage state without inventing a global protection score.

The first concrete read model is Device Watch. It combines retained normalized `CoverageSample` evidence with current sensor, ingestion, and controller-database operational health. Security findings remain a separate model: an unhealthy sensor is not itself a security incident, and an absence of findings is never proof that the network is safe.

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

## Aggregate coverage states

The detail read model uses the same current evidence and operational checks that feed lifecycle verification:

- `unverified` — required current evidence or first-collection validation is not available yet;
- `active-limited` — current passive neighbor-cache evidence exists, both expected ARP/NDP sources are available, and the sensor/ingestion/storage path is currently healthy, while Device Watch still has its declared observation-point limitations;
- `degraded` — a source, sensor collection, ingestion path, or controller-database capacity check is currently unhealthy, or retained coverage evidence is invalid/untrustworthy;
- `stale` — the latest successful collection/evidence is older than the bounded freshness window; and
- `disconnected` — the configured Device Watch runtime or ingestion path is no longer running.

`active-limited` is deliberately not named `healthy`, `protected`, or `full`. Even with both ARP and NDP sources current, Device Watch remains a local passive-neighbor capability.

A current sample with one working source and one unavailable source is **degraded**, not verified merely because some evidence still arrives. The working source remains visible as `current` in the detail response so partial usefulness is preserved without hiding the gap.

Operational causes take precedence over an otherwise-fresh evidence sample. For example, if the last sample is still inside its freshness window but the Device Watch runtime has stopped, the aggregate state is `disconnected`, while the old evidence remains visible underneath with its timestamp.

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

Cozy SOC does **not** currently relabel `unavailable` as `permission-required`: the passive source does not yet provide enough evidence to distinguish permission denial from a missing/failed system source.

## Sensor operational health

The configured Device Watch runtime is evaluated independently from the evidence payload:

- `current` — the runtime is running and its last successful collection is current;
- `starting` — the runtime is running but has not completed its first successful collection;
- `degraded` — the most recent collection attempt failed or the sensor timestamp is not trustworthy;
- `stale` — the runtime is still running but no successful collection has completed inside the freshness window;
- `disconnected` — the configured runtime is no longer running; and
- `unavailable` — the platform/runtime cannot provide Device Watch operational state.

The response includes the last attempt, last success, and a coarse bounded error class. Those fields describe collection health, not attack evidence.

## Ingestion health

The controller exposes current ingestion state separately from historical counters:

- `current` — no current pipeline problem is detected;
- queue pressure — depth is at least 75% of the known bounded queue capacity;
- backpressure — the current saturation episode has rejected/dropped evidence;
- write failure — the current ingestion episode contains a storage write failure;
- closing/closed — the ingestion path is shutting down or disconnected.

Cumulative dropped and failed totals remain visible for diagnostics, but old totals do not permanently poison current health after the pipeline recovers. The 75% queue threshold is an operational warning against a known finite queue, not a security/protection percentage.

A write failure can be caused by a full filesystem, SQLite quota exhaustion, or another storage problem. Cozy SOC currently reports the proven fact (`write-failed`) rather than guessing a more specific cause.

## Controller-database capacity

The SQLite store has a configured `max_page_count` quota. Device Watch storage health reports:

- allocated database bytes;
- actively used page bytes;
- reusable free-list page bytes; and
- the effective configured maximum bytes.

Quota pressure begins when **used** pages reach 90% of that known database quota; reaching the effective quota is a degraded state. Reusable free-list pages count as available headroom, so retention pruning does not falsely leave the database "almost full" simply because the file has not shrunk on disk.

This is **not filesystem free-space monitoring**. A healthy database-quota result does not prove that the host volume has enough free disk, and a database-quota warning does not mean the whole filesystem is full.

## Evidence validation

The read model strictly validates the Device Watch coverage evidence schema before presenting it or using it to verify the capability.

It requires:

- the expected schema version;
- one enrolled interface name;
- exactly one ARP and one NDP source entry;
- internally consistent non-negative observation counters;
- a status consistent with source availability; and
- `whole_network_traffic_visible=false`.

Malformed or contradictory evidence fails closed as degraded instead of being guessed healthy.

Stored free-form limitation strings are not copied into the frontend contract. The controller emits curated blind spots and next steps so a corrupted or hostile stored string cannot become authoritative UI guidance.

## Curated Device Watch blind spots

Every configured Device Watch report preserves these limitations:

1. **Host neighbor cache only** — only peers recently resolved by the enrolled Mac may appear. An unseen device is unknown, not proved absent or safe.
2. **Isolated segments are not observed** — client isolation and other VLANs/subnets may hide devices from this observation point.
3. **No traffic monitoring** — ARP/NDP evidence does not reveal other devices' uploads, east-west flows, packet contents, or application behavior.

Each gap includes one concrete next step. These are guidance, not automated response actions.

## Relationship to capability verification

Device Watch now declares five independent verification signals:

1. `network-scope-enrolled`;
2. `observation-freshness`;
3. `sensor-operational`;
4. `ingestion-health`; and
5. `storage-health`.

All must be fresh for the capability to be `verified`. This prevents a fresh historical sample from keeping the capability green after its sensor disconnects or its write path becomes unhealthy.

The lifecycle state and `device-watch.coverage` share the same operational evaluators. The UI therefore cannot say that a sensor is disconnected, stale, backpressured, or at database quota while `capabilities.list` independently calls the same capability verified.

A fresh sample may still contain zero neighbors. The sample itself is heartbeat/validation evidence that collection ran, so a quiet network is not automatically a source failure.

## Still outside this slice

This does not complete #12. Remaining coverage work includes explicit filesystem free-space measurement/diagnosis, measured ingestion latency rather than queue-pressure inference, other capability observation points, directionality, DNS/router-specific coverage, traffic-sensor gaps, wireless channel/dwell limits, and frontend presentation.
