# Foundation feasibility harness

This directory contains the disposable/reproducible experiments for [Foundation issue #5](https://github.com/fijimunkii/cozysoc/issues/5).

It is deliberately **not** the v0.1 application scaffold. The goal is to turn architecture assumptions into evidence before the product depends on them.

## What this harness proves

The checked-in Go CLI provides small, bounded primitives for:

- recording a non-sensitive host/interface snapshot;
- running an independently supervisable heartbeat process with sleep/resume gap detection;
- capturing a bounded number of packet summaries through the platform `tcpdump` path;
- normalizing those packet summaries into JSON observations; and
- hashing packet summaries by default so normal evidence collection does not dump addresses/content into logs.

CI proves that these primitives compile, are formatted/vetted, and pass unit tests. CI does **not** prove macOS service registration, switched-network visibility, Linux mirror/TAP behavior, or USB Wi-Fi compatibility.

Real-machine evidence is tracked separately in [issue #56](https://github.com/fijimunkii/cozysoc/issues/56). Until that evidence exists, the corresponding support-matrix entries remain Candidate/Untested.

## Toolchain

The experiment module targets Go 1.27. CI pins Go 1.27.1. The product's eventual Go version remains a separate implementation decision; this module is only a Foundation spike.

Build:

```bash
cd experiments/foundation/feasibility
go build -o bin/cozysoc-feasibility ./cmd/cozysoc-feasibility
```

Run the unit checks:

```bash
gofmt -w cmd
go vet ./...
go test ./...
```

## CLI

### Host snapshot

```bash
./bin/cozysoc-feasibility host
```

The snapshot intentionally omits interface addresses, MAC addresses, hostname, usernames, and other household identifiers.

### Service heartbeat

```bash
./bin/cozysoc-feasibility service \
  --heartbeat /tmp/cozysoc-heartbeats.jsonl \
  --interval 1s
```

Each record includes the PID, sequence, expected interval, and elapsed time from the prior heartbeat. An elapsed interval greater than three times the configured interval is marked as a gap. This gives sleep/resume and service-outage experiments a machine-readable signal instead of inferring continuity from a later process state.

Summarize gaps:

```bash
./bin/cozysoc-feasibility gaps --heartbeat /tmp/cozysoc-heartbeats.jsonl
```

### Bounded packet observation

List interfaces first with `host`, then capture a small authorized sample:

```bash
sudo ./bin/cozysoc-feasibility capture \
  --interface en0 \
  --count 20 \
  --timeout 15s \
  --source-id desktop \
  --scope-id owned-lab
```

The command invokes only the fixed `tcpdump` capture path with bounded arguments; it is not a generic command runner. Output contains a timestamp, source/scope/interface, summary length, and SHA-256 of the textual packet summary. The textual summary itself is omitted by default because it can contain household IP addresses and activity metadata.

Use `--show-summary` only for local interactive diagnosis. Do not commit raw/sensitive output.

To exercise normalization without live capture:

```bash
tcpdump -n -q -tt -r sample.pcap | \
  ./bin/cozysoc-feasibility normalize --interface fixture0
```

A fixture proves parser behavior only; it does not count as hardware/capture evidence for #5.

## Experiment 1 — macOS background-service lifetime

**Question:** can the Go controller run independently of the desktop window and expose a visible monitoring gap across sleep/resume?

The script [`scripts/macos-launchd-lifetime.sh`](scripts/macos-launchd-lifetime.sh) installs the probe as a user LaunchAgent for a lifecycle experiment. This is intentionally narrower than the final application packaging path.

Build the probe and use absolute paths:

```bash
./scripts/macos-launchd-lifetime.sh install \
  "$PWD/bin/cozysoc-feasibility" \
  "$PWD/evidence/local/macos-heartbeats.jsonl"
```

Then test, recording sanitized results in a copy of [`evidence/TEMPLATE.md`](evidence/TEMPLATE.md):

1. close the terminal (and later the feasibility UI when one exists) and verify the service remains alive;
2. query `status` and record PID/state;
3. terminate the service process and verify launchd restart behavior;
4. reboot and verify the service returns;
5. sleep long enough to cross the heartbeat gap threshold, resume, and run `gaps`;
6. test denied/failed registration behavior separately; and
7. uninstall and verify the LaunchAgent and process are gone.

Cleanup:

```bash
./scripts/macos-launchd-lifetime.sh uninstall
```

### Important limitation

This validates **launchd-owned process lifetime**, not the final app-bundled registration path. The proposed desktop architecture uses Apple's modern ServiceManagement/`SMAppService` model for app-bundled services on macOS 13+. Final Tauri bundle + `SMAppService` registration/signing/upgrade behavior remains real-machine evidence in #56.

Tauri can package target-specific external binaries, but its sidecar spawn API is not itself the production controller lifecycle. Packaging and runtime ownership remain separate architecture concerns.

## Experiment 2 — desktop packet-visibility boundary

**Question:** what does an ordinary desktop connection actually observe?

### Positive host-local test

1. start `capture` on the active desktop interface;
2. generate traffic from the desktop itself to an owned target;
3. verify normalized observations arrive; and
4. record the interface, OS, capture permissions, command, observation count, and any drops/errors.

### Switched-network negative test

Use three owned hosts on an ordinary switched LAN: **A**, **B**, and the Cozy SOC capture desktop **C**.

1. capture on C;
2. generate a known unicast exchange between A and B that is not addressed to C;
3. verify whether C sees the A↔B unicast packets;
4. repeat with promiscuous mode if the platform tool exposes it; and
5. record the result without committing raw packet content.

A normal switched desktop attachment is expected not to receive arbitrary unicast conversations between other hosts. A failure to see A↔B traffic is therefore a **successful boundary test**, not a broken sensor.

Do not use ARP poisoning, MAC flooding, Wi-Fi deauthentication, or other traffic-interception tricks for this experiment.

## Experiment 3 — Linux mirrored/TAP traffic sensor

**Question:** when traffic is deliberately delivered to the sensor, what scope and direction are actually visible?

On an owned Linux lab host with a dedicated observation interface:

1. configure an owned switch mirror/SPAN, passive TAP, or other explicitly chosen observation path;
2. record the switch/TAP model and exact configuration outside sensitive household identifiers;
3. use `host` to record the sensor interface and build metadata;
4. capture bounded traffic with the CLI;
5. generate controlled IPv4 and IPv6 flows from owned clients in both directions;
6. verify which directions/devices/VLAN tags appear;
7. deliberately remove or misconfigure the mirror path and verify observations stop/degrade;
8. restore the path and verify recovery; and
9. record packet-drop/overrun indicators from the selected capture/IDS path where available.

Monitor/SPAN behavior is hardware-dependent: some switches may omit VLAN tags, duplicate frames, alter timing, or drop mirrored packets under load. Record what the chosen hardware actually does rather than generalizing from a successful process start.

## Experiment 4 — dedicated USB Wi-Fi / Kismet

**Question:** can a documented Linux USB radio enter a stable monitor-mode capture path without disrupting management connectivity?

Run the preflight before Kismet:

```bash
./scripts/linux-wifi-preflight.sh wlan1
```

The script refuses the current IPv4/IPv6 default-route interface. A **dedicated adapter is the default test topology** because Kismet may need to disable non-monitor interfaces on the same radio for reliable channel control.

Record at minimum:

- USB vendor/product ID;
- retail model plus hardware revision if known;
- chipset;
- firmware and kernel driver;
- Linux distribution/kernel;
- Kismet version;
- bands/channels the driver reports;
- monitor-mode creation;
- channel-hopping behavior;
- unplug/replug recovery;
- cleanup/restoration; and
- proof that normal management connectivity used a different interface.

Current Kismet documentation lists MediaTek `mt7612u` 802.11ac devices among its best-supported Linux options, but that is a **chipset candidate**, not a Cozy SOC compatibility claim for every retail adapter containing it.

One radio cannot continuously observe every channel: channel hopping trades complete per-channel capture for broader environmental coverage. Evidence must record the actual channel list and hop behavior rather than describing a hopping radio as continuous all-channel coverage.

No injection, deauthentication, credential cracking, or offensive Wi-Fi behavior is part of this experiment.

## Experiment 5 — native vs containers

For each attempted deployment, record what observation primitive it actually receives.

### Docker Desktop

Docker Desktop host networking is useful for TCP/UDP service reachability, but its documented host-network implementation works at layer 4 and does not give containers direct access to host interfaces. It is therefore **not** our raw physical-interface capture solution on macOS/Windows.

### Native desktop helper/service

Use native/platform capture and service primitives for capabilities that need direct host interfaces or OS lifecycle integration.

### Linux sensor

Linux remains the reference candidate for advanced packet/wireless observation because the sensor can run directly against the interfaces/drivers being validated. Containers may still be useful for higher-level services after their privileges and observation path are proven; containerization is not itself evidence of visibility or isolation quality.

## Evidence status

| Test | Repository/CI status | Real-machine status |
| --- | --- | --- |
| Go probe build/unit/vet | Tested in CI | N/A |
| Heartbeat gap logic | Tested in CI | Service sleep/reboot pending #56 |
| tcpdump normalization/redaction | Tested in CI | Live desktop capture pending #56 |
| macOS launchd lifetime | Harness present | Untested pending #56 |
| Tauri bundle + `SMAppService` registration | Not implemented here | Untested pending #56 |
| Switched-LAN negative visibility | Procedure present | Untested pending #56 |
| Linux mirror/TAP visibility | Procedure present | Untested pending #56 |
| USB Wi-Fi/Kismet | Safety preflight present | Untested pending #56 |

Issue #5 must remain open while any release-driving hardware/platform claim is still untested.

## Evidence handling

- Keep local/raw output under `evidence/local/`; it is ignored by Git.
- Commit only sanitized evidence needed to support a decision.
- Do not commit packet payloads, browsing/DNS history, household IP/MAC addresses, SSIDs, credentials, device serial numbers, or unrelated USB identifiers.
- Prefer counts, hashes, exact software/hardware versions, test topology, and pass/fail observations.

## Current-source constraints

Research refreshed 2026-09-08:

- Apple `SMAppService`: <https://developer.apple.com/documentation/servicemanagement/smappservice>
- Tauri external binaries: <https://v2.tauri.app/develop/sidecar/>
- Docker host networking / Desktop limitations: <https://docs.docker.com/engine/network/drivers/host/>
- Wireshark switched-Ethernet capture boundaries: <https://wiki.wireshark.org/CaptureSetup/Ethernet>
- Kismet Linux Wi-Fi: <https://www.kismetwireless.net/docs/readme/datasources/wifi-linux/>
- Kismet channels/hopping: <https://www.kismetwireless.net/docs/readme/datasources/channelhop/>

These sources explain upstream capabilities/constraints. Cozy SOC support still requires our own evidence on named hardware and software versions.
