# Coverage and sensor-health read model

Issue #12 turns retained evidence into user-facing coverage state without inventing a global protection score.

The first concrete read model is Device Watch. It is intentionally built from the existing normalized `CoverageSample` rather than from process state, alert counts, or a second health database.

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

The detail read model uses the same current evidence that feeds lifecycle verification:

- `unverified` — no retained current Device Watch coverage evidence exists yet;
- `active-limited` — current passive neighbor-cache evidence exists within Device Watch's declared limited scope;
- `degraded` — current sources are unavailable or the retained coverage evidence is invalid/untrustworthy;
- `stale` — the newest valid evidence is older than the bounded freshness window.

`active-limited` is deliberately not named `healthy`, `protected`, or `full`. Even with both ARP and NDP sources current, Device Watch remains a local passive-neighbor capability.

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

The lifecycle state and this detail endpoint share one evaluator.

That matters because the UI must never say that evidence is invalid or stale while `capabilities.list` independently says the same evidence is verified. Invalid evidence now degrades the existing `observation-freshness` verification signal too.

A fresh `partial` sample may still contain zero neighbors. The sample itself is heartbeat/validation evidence that collection ran, so a quiet network is not automatically a source failure.

## Still outside this slice

This does not complete #12. Remaining coverage work includes richer sensor/disconnection and ingestion-health evidence, storage-pressure degradation, other capability observation points, directionality, DNS/router-specific coverage, traffic-sensor gaps, wireless channel/dwell limits, and frontend presentation.
