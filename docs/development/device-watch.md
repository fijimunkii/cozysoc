# Device Watch passive neighbor discovery

Issue #11 begins with the narrowest useful discovery source: the operating system's existing IPv4 ARP and IPv6 NDP neighbor tables for one explicitly enrolled local interface.

This is **presence evidence, not whole-network traffic visibility**. A host neighbor cache only contains peers the controller host has recently resolved on its local link. Client isolation, other VLANs/subnets, sleeping devices, stale cache entries, and devices that have never communicated with the host can all be absent.

## Scope binding

Device Watch never chooses a network on its own.

Enrollment captures a `device_watch` binding inside `NetworkScope.metadata` with:

- the exact interface name;
- the interface index observed at enrollment; and
- the set of usable IPv4/IPv6 prefixes present at enrollment.

Loopback and point-to-point interfaces are rejected by this first slice so a VPN/tunnel is not accidentally treated as the home LAN. Before every collection, the current interface must still be up, retain the same name/index, and share at least one enrolled prefix. A mismatch blocks collection with a scope-revalidation error rather than silently following the machine onto a new network.

Neighbors are filtered again against the enrolled prefixes before persistence. New address families/prefixes that were not part of the enrolled binding are therefore not silently added to scope.

## macOS source

The v0.1 reference implementation is macOS-first, matching the current support matrix.

It invokes only two fixed, read-only system utilities without a shell:

- `/usr/sbin/arp -an -i <enrolled-interface>` for IPv4 ARP cache entries; and
- `/usr/sbin/ndp -an` for IPv6 NDP entries, filtered to the enrolled interface after parsing.

`-n` keeps ARP output numeric and avoids hostname resolution. The interface name is separately validated and passed as one argument; manifests/config cannot supply a command, executable path, or arbitrary arguments.

Command output is capped at 1 MiB, individual parsed lines are bounded, and a snapshot may contain at most 4096 neighbors. Incomplete entries and entries without a valid unicast link-layer address are ignored because they do not establish a visible peer.

One source may be unavailable while the other remains usable; the coverage evidence records source availability independently. If neither source is available, Device Watch records an `unavailable` coverage sample and emits no device-arrival/departure inference.

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

## No departure inference yet

This slice intentionally emits only positive `seen` evidence. A missing neighbor in the next cache snapshot is **not** a departure event.

That is important for sleep/resume, cache expiry, Wi-Fi roaming, temporary IPv6 addresses, and client isolation. Presence-state transitions will be added only after the observation history can distinguish a source gap from a meaningful absence interval.

## What remains in #11

This PR does not close #11. Remaining work includes:

- controller/store wiring for enrolled scopes and the Device Watch sensor;
- periodic scheduling and lifecycle-driver registration;
- temporal identity-claim/device reconciliation from neighbor observations;
- visible-now / last-seen / uncertain-offline presence projection;
- labeling/correction flows;
- service-discovery enrichment where justified;
- conservative, consented active probes only if passive evidence proves insufficient; and
- owned-lab evidence across IPv4-only, dual-stack, isolation, sleep/resume, address changes, and permission/source failures.
