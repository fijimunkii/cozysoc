# Device Watch passive neighbor discovery

Issue #11 begins with the narrowest useful discovery source: the operating system's existing IPv4 ARP and IPv6 NDP neighbor tables for one explicitly enrolled local interface.

This is **presence evidence, not whole-network traffic visibility**. A host neighbor cache only contains peers the controller host has recently resolved on its local link. Client isolation, other VLANs/subnets, sleeping devices, stale cache entries, and devices that have never communicated with the host can all be absent.

## Explicit network enrollment

Device Watch never chooses a network on its own.

The authenticated `networks.list` API (CLI: `cozysoc networks`) lists local interfaces that are currently eligible for enrollment. Candidate discovery reads only local interface/address metadata through Go's networking APIs. It does not ping, resolve names, scan ports, enumerate an IPv6 address space, or otherwise send network traffic.

The authenticated `network.enroll` mutation (CLI: `cozysoc network-enroll INTERFACE`) captures the selected interface **at execution time**. Enrollment stores a `device_watch` binding inside `NetworkScope.metadata` containing:

- the exact interface name;
- the interface index observed at enrollment; and
- the canonical set of usable IPv4/IPv6 prefixes present at enrollment.

Loopback, down, and point-to-point interfaces are rejected so VPN/tunnel interfaces are not accidentally authorized as the home LAN. The controller derives the prefix set itself; the caller cannot supply arbitrary CIDRs.

v0.1 permits one active Device Watch scope. Re-enrolling the exact same binding returns the existing scope as an idempotent no-op. Attempting to enroll a different network while one is active returns a conflict rather than silently replacing or broadening authorization. Concurrent enrollment attempts are serialized.

A real enrollment and its durable `network-scope-enroll` audit event commit in one SQLite transaction. If the audit cannot commit, the scope is rolled back too.

**Enrollment alone does not enable Device Watch.** It creates only the durable authorization object.

Before every collection, the current interface must still be up, retain the enrolled name/index, and share at least one enrolled prefix. A mismatch blocks collection with a scope-revalidation error rather than silently following the machine onto a new network. Neighbors are filtered again against the enrolled prefixes before persistence; newly encountered prefixes are not silently added to scope.

## Explicit enable / disable

The authenticated lifecycle controls are capability-specific:

```bash
cozysoc device-watch-enable --state-dir PATH
cozysoc device-watch-disable --state-dir PATH
```

`device-watch.enable` does not accept a caller-supplied scope. It selects the single already-enrolled Device Watch scope and builds the typed capability configuration internally.

Before enabled intent is persisted, the real Device Watch lifecycle driver performs side-effect-free preflight. Enable preflight checks:

- the passive runtime is supported on the current platform;
- a `network_scope_id` is present;
- the scope still exists and contains a valid Device Watch binding; and
- the enrolled interface identity/prefixes still match the current machine.

If preflight is blocked, the request fails before writing enabled intent or a requested transition audit. On success, the controller records the request, atomically persists desired state, updates the in-memory capability model, and invokes the same lifecycle engine used for startup reconciliation.

A failed enable is compensated: Cozy SOC attempts to stop any runtime side effect and restores the previous durable configuration. A repeated enable for the same already-running scope is idempotent.

Disable is intentionally easier than enable. It never requires the network to still be present or healthy. Desired state is written as `disabled` before runtime stop, so a failed stop or abrupt controller restart cannot cause Device Watch to come back merely because the previous runtime was still alive. Failed disable also triggers an emergency stop attempt.

`enabled` still does **not** mean verified coverage. Device Watch is builtin, so process state remains `not-applicable`; runtime activity is reported separately by the control result. Verification advances only from current evidence and current operational health as described below.

## Evidence and operational verification

Device Watch re-evaluates five declared verification signals while durable desired state is `enabled`. Reads such as `capabilities.list` and `device-watch.coverage` remain side-effect-free; the controller performs verification reconciliation independently.

The signals are:

1. `network-scope-enrolled` — the stored scope still resolves to the enrolled interface binding;
2. `observation-freshness` — the newest retained Device Watch `CoverageSample` is current, internally valid, and reports both expected ARP/NDP sources available;
3. `sensor-operational` — the configured runtime is running and completing collections within the freshness window;
4. `ingestion-health` — accepted evidence is not measurably lagging and the bounded ingestion path has no active backpressure/drop, generic write failure, or typed SQLite-full failure; and
5. `storage-health` — the controller database quota and, where supported, the host volume containing the state directory both have current capacity evidence without pressure/full state.

All five must be fresh before the lifecycle state becomes `verified`. A current evidence sample therefore cannot keep Device Watch green after its producer disconnects or its measured write/capacity path becomes unhealthy.

Coverage freshness is based on `CoverageSample.EndedAt`, not insertion order. Replaying an old sample later therefore cannot make historical evidence look current. With the one-minute collection cadence, the initial bounded freshness window is three minutes: current evidence tolerates ordinary scheduling jitter, while multiple missed collections become `stale`.

Current Device Watch evidence maps as follows:

- no retained sample: `missing`, leaving verification `unverified`;
- fresh evidence with both expected ARP and NDP sources available: evidence freshness passes for the **limited passive neighbor-cache capability**;
- fresh evidence with either expected source unavailable: evidence freshness fails, making verification `degraded` while the still-working source remains visible in coverage detail;
- fresh evidence with neither source available: failed/degraded;
- evidence older than the freshness window: `stale`; and
- unsupported, contradictory, or implausibly future evidence: failed rather than guessed healthy.

Sensor health is evaluated separately from evidence. A runtime can be `starting`, `current`, `degraded`, `stale`, `disconnected`, or `unavailable`. A failed collection records only a coarse bounded error class; it is operational evidence, not a security finding.

The ingestion path reports queue depth/capacity and `queue_pressure` as utilization telemetry, plus measured accepted-to-durable latency, queue wait, storage-processing time, pending age, recent slow streak, active backpressure/write-failure state, an optional bounded failure class, and cumulative drop/failure totals. Queue pressure begins at 75% of the bounded queue but no longer degrades verification by itself when measured durable latency remains current.

The initial measured-lag rule uses a 5-second threshold: the pipeline degrades when the oldest accepted pending record reaches that age, or when three consecutive recent successful durable completions each take at least 5 seconds. The 5-second value is provisional operational guidance anchored to half of the existing 10-second storage-operation timeout; it is not a measured hardware SLO. Recent latency samples age out after one minute when the pipeline is quiet, and one later fast successful write resets the slow streak.

SQLite `FULL` is detected from the typed SQLite result code and reported as `sqlite-full`; Cozy SOC does not parse a human-readable error message or expose raw database error text. Historical totals remain available for diagnosis but do not permanently degrade a recovered pipeline.

A current `sqlite-full` episode does not clear merely because disk/quota capacity later appears healthy. It stays degraded until a subsequent successful storage operation proves that the write path recovered.

Storage health keeps the controller database quota separate from host-volume capacity. Database detail still reports allocated bytes, actively used page bytes, reusable free-list bytes, and the effective SQLite `max_page_count` capacity; the 90% quota warning applies to **used** quota, so reusable pages recovered through retention remain usable headroom.

On Darwin and Linux the same storage detail also reports filesystem total bytes and bytes currently available to the process for the volume containing the state directory. Host-volume `pressure` begins at 128 MiB of remaining available headroom, while `full` is reserved for the operating system reporting zero available bytes. The 128 MiB threshold is operational guidance, not a prediction of the next write failure or a security score.

When a typed SQLite-full write coincides with current filesystem-full evidence, coverage reports `storage-filesystem-full`; when the database quota is reached, it reports `storage-quota-reached`. If current capacity no longer explains the prior SQLite-full failure, coverage remains `ingestion-sqlite-full` until a successful write proves recovery. Unsupported filesystem introspection is exposed without guessing; a supported platform whose capacity probe currently fails is `filesystem-unknown` and degrades storage verification.

A successful current sample may contain zero neighbors. That is still current heartbeat/validation evidence that the passive collection ran; a quiet network is not automatically treated as sensor failure.

`verified` here is deliberately scoped. It means the declared Device Watch evidence and its current operational path satisfy the manifest. It does **not** mean every LAN client is visible, other devices' internet traffic is observed, every VLAN is covered, or the household is globally "protected." The coverage sample continues to record `whole_network_traffic_visible=false` and the known passive-cache limitations.

### Shared coverage projection

`device-watch.coverage` keeps its existing Device-Watch-specific fields and also returns a nested capability-independent `coverage` report described in [`coverage-contract.md`](coverage-contract.md). The legacy aggregate state/reason/next-step and the shared report come from the same validated evaluator result.

The live Device Watch projection uses one observation point, `device-watch.local-neighbor-cache`, with kind `host-neighbor-cache`. It represents the configured network and expected IPv4/IPv6 families, marks address families verified independently from current ARP/NDP evidence, uses the one-minute periodic cadence, and maps the curated blind spots into explicit shared gaps.

The observation point declares **no observed traffic directions**. Its `no-traffic-monitoring` gap explicitly identifies ingress, egress, and east-west as traffic directions not established by Device Watch. Operational degradation such as measured ingestion lag can make the point degraded without erasing the latest address-family evidence that was actually verified.

Before the first trusted coverage sample, the shared projection does not invent an interface value merely because the enrollment record exists elsewhere. It exposes the configured network and expected IPv4/IPv6 dimensions immediately and adds the interface once that fact is present in the trusted Device Watch coverage report.

## macOS passive source

The v0.1 observation source is macOS-first, matching the current support matrix.

It invokes only two fixed, read-only system utilities without a shell:

- `/usr/sbin/arp -an -i <enrolled-interface>` for IPv4 ARP cache entries; and
- `/usr/sbin/ndp -an` for IPv6 NDP entries, filtered to the enrolled interface after parsing.

`-n` keeps ARP output numeric and avoids hostname resolution. The interface name is separately validated and passed as one argument; manifests/config cannot supply a command, executable path, or arbitrary arguments.

Command output is capped at 1 MiB, individual parsed lines are bounded, and a snapshot may contain at most 4096 neighbors. Incomplete entries and entries without a valid unicast link-layer address are ignored because they do not establish a visible peer.

One source may be unavailable while the other remains usable; coverage evidence records source availability independently. A current gap in either expected source degrades Device Watch verification. If neither source is available, Device Watch records an `unavailable` coverage sample and emits no device-arrival/departure inference.

## Controller runtime

When Device Watch is explicitly enabled for a valid stored scope, the lifecycle driver creates or reuses one deterministic built-in sensor identity for that scope/interface and starts a one-minute passive collection loop.

The loop:

1. revalidates the enrolled interface and prefixes;
2. reads the passive ARP/NDP snapshot;
3. writes observations and coverage through the bounded #10 ingestion queue; and
4. reconciles each persisted neighbor observation into temporal identity evidence.

Startup reconciliation uses the same lifecycle driver as live enablement. A persisted enabled intent is never implemented through a separate manual runtime-start path.

The runtime is stoppable and controller shutdown waits for it before closing ingestion/storage. Startup or collection failure does not terminate the controller. The runtime records a coarse failure class (`scope-mismatch`, `source-unavailable`, and similar), while current coverage and operational evidence are independently re-evaluated into verification state. A current source gap or collection failure degrades Device Watch; a running sensor that stops succeeding becomes stale; a stopped configured runtime is reported disconnected.

A controller restart safely reuses the same deterministic Device Watch sensor. Replayed observations and reconciliation are idempotent.

## Normalized observations

Each in-scope neighbor becomes a `device-neighbor-seen` observation containing only:

- numeric IP address;
- normalized link-layer address;
- enrolled interface;
- IPv4/IPv6 family;
- source method (`arp-cache` or `ndp-cache`); and
- bounded source state when available.

No hostname lookup, manufacturer lookup, packet payload, or service scan is performed.

Neighbor observations use a deterministic one-minute source bucket. Repeated snapshots of the same `(scope, sensor, interface, method, IP, MAC)` within that bucket replay as the same source event and are deduplicated by the #10 storage contract. A later bucket creates fresh presence evidence for first/last-seen history.

Every collection also emits a `device-watch` coverage sample. Even a fully successful ARP+NDP snapshot is marked `partial`; its evidence explicitly sets `whole_network_traffic_visible=false` and carries the passive-cache limitations above. The `partial` sample status describes Device Watch's intrinsically limited observation point; source availability inside the evidence determines whether current evidence is merely limited or additionally degraded.

## Temporal identity reconciliation

Device Watch does not make IP addresses or MAC addresses permanent device identifiers.

For every persisted neighbor observation it creates immutable temporal claims for:

- the observed IP address; and
- the observed MAC/link-layer address.

The IP claim is never used by itself to reconnect a device identity, preventing DHCP/address reuse from merging unrelated devices.

MAC continuity is treated as an inference. If exactly one device has matching retained MAC evidence within the previous seven days, the new observation extends that device's temporal evidence chain. If no device matches, Device Watch creates a new candidate Device. If multiple devices match, the current claims remain preserved but unlinked and the result stays ambiguous rather than selecting a winner.

A locally administered/private MAC receives lower inference confidence than a globally administered address. User corrections and future stronger sources can still merge or split Device records without deleting the original observations or claims.

Reconciliation uses deterministic claim/link/device identifiers for crash recovery. If the controller stops after writing the raw observation but before all identity links are written, replay safely completes the same reconciliation rather than creating duplicate identity history.

## Presence projection

Device presence currently has two passive states:

- `visible`: positive Device Watch evidence was observed within the last three minutes; and
- `uncertain`: the latest positive evidence is older than that freshness window.

The projection exposes the preserved first-seen and latest-seen timestamps through the authenticated `devices.list` API and `devices` CLI command. It deliberately has **no automatic `offline` state** yet.

A missing neighbor in the next cache snapshot is not a departure event. Sleep/resume, cache expiry, Wi-Fi roaming, temporary IPv6 addresses, client isolation, and a controller source gap all therefore age a device to `uncertain` instead of generating a false leave/rejoin sequence.

## User labels

`device.label` is a bounded state-changing local API operation. The corresponding CLI surface is:

```bash
cozysoc device-label --state-dir PATH DEVICE_ID "Living Room TV"
```

An empty label clears the user label. Labels are user metadata only; they do not alter the underlying temporal identity claims or increase inference confidence.

Authorization follows the **current durable Device Watch intent**, not a scope cached at controller startup. Storage independently requires retained identity evidence for that device in the same scope before it permits the update. A guessed device ID from another scope therefore cannot be labeled through this method.

Labels are bounded, trimmed, and reject control characters. A real change and its `device-label` audit event commit in one SQLite transaction; an identical repeated label is an idempotent no-op and does not create another state-transition audit event.

The mutation does not grant network, generic capability-lifecycle, process, filesystem, or arbitrary database write authority.

## What remains in #11

This work still does not close #11. Remaining work includes:

- desktop UI exposure for network selection, enable/disable, device presence, labeling, and coverage/operational detail;
- auditable merge/split correction flows for identity ambiguity;
- optional service-discovery enrichment where justified;
- conservative, consented active probes only if passive evidence proves insufficient; and
- owned-lab evidence across IPv4-only, dual-stack, isolation, sleep/resume, address changes, enrollment changes, enable/disable, labeling, permission/source failures, runtime disconnection, ingestion lag, and write-pressure/full-volume recovery scenarios.

The shared #12 coverage vocabulary can now represent permission-required state, other observation points, traffic direction gaps, DNS client scope, and wireless hopping/dwell limits. Broader #12 work still requires **real producers** for those capabilities, source-specific permission evidence, aggregation once multiple real observation points exist, and generic frontend presentation. Named-workload calibration for latency/resource thresholds and real low-disk/full-volume recovery behavior on named filesystems/hardware remain #29 lab claims rather than things CI fixtures can certify.
