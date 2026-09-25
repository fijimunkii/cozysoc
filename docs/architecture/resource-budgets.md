# Architecture resource budgets

These values are **acceptance targets to measure**, not benchmark results or product claims. Issue #5 establishes the first measurements; issue #29 turns applicable budgets into release gates.

The budgets intentionally separate the core product from optional engines such as Suricata or Kismet. An optional engine cannot hide its resource cost inside the base-app number.

## Reference workload for core measurements

Unless a test says otherwise, the initial core workload should model:

- one enrolled home network;
- up to 100 represented devices/identity records;
- Device Watch enabled;
- coverage/health samples active;
- no Suricata, Kismet, Wazuh, OpenCanary, or managed DNS engine;
- no full packet capture retention; and
- enough synthetic/owned-lab observations to exercise normal persistence and UI queries.

The actual reference hardware and OS are recorded by #5.

## Core controller targets

| Metric | Initial target | Measurement notes |
| --- | ---: | --- |
| Idle CPU after warm-up | median <= 1% of one logical core over 15 minutes | No UI interaction; normal health/discovery schedule |
| Background CPU p95 | <= 5% of one logical core under the reference discovery workload | Short startup/migration spikes measured separately |
| Resident memory | <= 128 MiB steady-state | Controller + embedded DB library; excludes desktop WebView and optional engines |
| Process start to local API healthy | <= 2 seconds | Begins after OS service manager starts the process; OS approval/install time excluded |
| Recovery after ordinary controller crash | <= 10 seconds target | Subject to platform service-manager backoff policy; crash loop must remain bounded |
| Unbounded goroutine/thread/queue growth | 0 tolerated | Sustained tests must reach steady state |

If the implementation cannot meet a target on reasonable reference hardware, adjust the architecture or record a deliberate budget revision with measurements. Do not silently redefine the workload.

## Desktop shell/UI targets

| Metric | Initial target | Notes |
| --- | ---: | --- |
| Shell + WebView incremental resident memory | <= 256 MiB steady-state | Excludes controller and optional engines |
| Launch to usable cached UI when controller is already healthy | <= 3 seconds | Includes initial API handshake and first view render |
| Controller lifetime coupling | 0 | Closing/crashing UI must not stop controller |

The UI is allowed to consume more resources while visibly open than the background controller, but it should disappear from the background budget when closed.

## Core storage targets

Storage policy will be finalized by #30; these are architecture envelopes for #10/#5 to test.

| Metric | Initial target | Notes |
| --- | ---: | --- |
| Discovery-only normalized data growth | <= 30 MiB/day for the reference workload | Excludes optional DNS/flow/IDS/wireless data |
| Default core managed-data quota | <= 1 GiB before pressure policy must act | Exact per-class retention is #30 |
| Full packet payload retention | 0 by default | Diagnostic captures, if introduced later, are explicit and separately bounded |
| Database access | controller only | UI/engines/sensors do not open the DB file directly |

Storage pressure must degrade explicitly: retention/compaction should run before writes fail, and data loss/expiry must be represented rather than turning old data into current evidence.

### Discovery growth budget decision

The discovery target is revised from **25 to 30 MiB/day** by maintainer decision.
The [sparse replay adapter measurement](../development/device-watch-batch-adapter-workload.md#sparse-replay-index-september-13-2026)
uses 100 devices and 1,440 one-minute collections after warm-up on an Apple M4
Mac16,13 (10 logical CPUs, 24 GiB RAM), macOS 26.6.2 and APFS. It completed in
484,099 ms and grew by 28.508 MiB, leaving **1.492 MiB** under the revised target.
The 20% increase accepts this measured component footprint and prioritizes live
integration over further compaction solely to meet 25 MiB. It does not establish
that the original target was impossible.

The workload, collection cadence, evidence fidelity, durability, retention and
1 GiB quota are unchanged. The canonical batch writer and read view are now the
live Device Watch path. The last completed full-controller workload measured the
earlier writer at 474.50 MiB/day; the live path needs its own full workload result
before a 30 MiB/day claim. Historical evidence retains the target used when it
was recorded.

## Optional capability budgets

Every managed engine/capability added after the core must publish its own measured envelope:

- idle and active CPU;
- resident memory;
- disk growth and retained data classes;
- network egress/update behavior;
- expected event throughput and drop behavior; and
- hardware assumptions.

Examples: a Suricata sensor budget depends on link throughput and rules; Kismet depends on radios/channel plan. Those numbers must come from named test hardware/settings rather than generic marketing requirements.

## Network activity budget principles

The base app has no continuous product telemetry requirement.

- LAN discovery/probes are rate/concurrency bounded and scoped to enrolled networks.
- External quality probes are explicit, low-rate, and disable-able.
- Update/feed checks identify their outbound destination and cadence.
- Offline operation remains functional with visibly stale updates where applicable.
- Bulk speed tests and diagnostic uploads are opt-in, not background defaults.

## Revising budgets

A budget revision should include:

1. old and new values;
2. reference hardware/OS;
3. exact workload and test duration;
4. why the original target was unrealistic or no longer representative; and
5. whether the change affects installation recommendations or release claims.
