# Device Watch passive neighbor discovery

Issue #11 begins with the narrowest useful discovery source: the operating system's existing IPv4 ARP and IPv6 NDP neighbor tables for one explicitly enrolled local interface.

This is **presence evidence, not whole-network traffic visibility**. A host neighbor cache only contains peers the controller host has recently resolved on its local link. Client isolation, other VLANs/subnets, sleeping devices, stale cache entries, and devices that have never communicated with the host can all be absent.

## Scope binding

Device Watch never chooses a network on its own.

Enrollment captures a `device_watch` binding inside `NetworkScope.metadata` with:

- the exact interface name;
- the interface index observed at enrollment; and
- the set of usable IPv4/IPv6 prefixes present at enrollment.

Loopback and point-to-point interfaces are rejected so a VPN/tunnel is not accidentally treated as the home LAN. Before every collection, the current interface must still be up, retain the same name/index, and share at least one enrolled prefix. A mismatch blocks collection with a scope-revalidation error rather than silently following the machine onto a new network.

Neighbors are filtered again against the enrolled prefixes before persistence. New address families/prefixes that were not part of the enrolled binding are therefore not silently added to scope.

The controller runtime does not invent or auto-enroll a scope. Device Watch remains dormant unless capability intent is explicitly `enabled` with a `network_scope_id` that already exists in controller-owned storage and contains a valid Device Watch binding. The future enrollment mutation/API remains separate work.

## macOS source

The v0.1 reference implementation is macOS-first, matching the current support matrix.

It invokes only two fixed, read-only system utilities without a shell:

- `/usr/sbin/arp -an -i <enrolled-interface>` for IPv4 ARP cache entries; and
- `/usr/sbin/ndp -an` for IPv6 NDP entries, filtered to the enrolled interface after parsing.

`-n` keeps ARP output numeric and avoids hostname resolution. The interface name is separately validated and passed as one argument; manifests/config cannot supply a command, executable path, or arbitrary arguments.

Command output is capped at 1 MiB, individual parsed lines are bounded, and a snapshot may contain at most 4096 neighbors. Incomplete entries and entries without a valid unicast link-layer address are ignored because they do not establish a visible peer.

One source may be unavailable while the other remains usable; the coverage evidence records source availability independently. If neither source is available, Device Watch records an `unavailable` coverage sample and emits no device-arrival/departure inference.

## Controller runtime

When Device Watch is enabled for a valid stored scope, the controller creates or reuses one deterministic built-in sensor identity for that scope/interface and starts a one-minute passive collection loop.

The loop:

1. revalidates the enrolled interface and prefixes;
2. reads the passive ARP/NDP snapshot;
3. writes observations and coverage through the bounded #10 ingestion queue; and
4. reconciles each persisted neighbor observation into temporal identity evidence.

Startup or collection failure does not terminate the controller. The runtime records only a coarse failure class (`scope-mismatch`, `source-unavailable`, and similar) and leaves capability verification unchanged. Starting the producer is **not** equivalent to satisfying Device Watch coverage verification; #12 remains responsible for turning fresh coverage evidence into capability verification state.

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

Every collection also emits a `device-watch` coverage sample. Even a fully successful ARP+NDP snapshot is marked `partial`; its evidence explicitly sets `whole_network_traffic_visible=false` and carries the passive-cache limitations above.

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

`device.label` is the first bounded state-changing local API operation. The corresponding CLI surface is:

```bash
cozysoc-controller device-label --state-dir PATH DEVICE_ID "Living Room TV"
```

An empty label clears the user label. Labels are user metadata only; they do not alter the underlying temporal identity claims or increase inference confidence.

Authorization remains tied to the persisted Device Watch scope. The controller supplies only the configured `network_scope_id`, and storage independently requires retained identity evidence for that device in the same scope before it permits the update. A guessed device ID from another scope therefore cannot be labeled through this method.

Labels are bounded, trimmed, and reject control characters. A real change and its `device-label` audit event commit in one SQLite transaction; an identical repeated label is an idempotent no-op and does not create another state-transition audit event.

The mutation does not grant network, capability-lifecycle, process, filesystem, or arbitrary database write authority.

## What remains in #11

This work still does not close #11. Remaining work includes:

- authenticated network enrollment/configuration mutation and user-facing scope selection;
- lifecycle-driver registration and #12 coverage-verification wiring;
- desktop UI exposure for the existing device/presence and labeling contracts;
- auditable merge/split correction flows for identity ambiguity;
- optional service-discovery enrichment where justified;
- conservative, consented active probes only if passive evidence proves insufficient; and
- owned-lab evidence across IPv4-only, dual-stack, isolation, sleep/resume, address changes, labeling, and permission/source failures.
