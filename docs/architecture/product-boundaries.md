# Product boundaries and journeys

## Product promise

Cozy SOC is a local-first home-network security hub that helps a household answer:

1. **What is connected?**
2. **What looks unusual?**
3. **What should I do next?**

It should make specialist capabilities approachable without pretending a desktop app can observe traffic that never reaches it.

## Product boundaries

### Cozy SOC is

- a desktop-first interface backed by an independently running controller;
- a normalized view of devices, activity, findings, coverage, and health from multiple observation sources;
- a guided installer/connector for a curated set of security capabilities;
- an upgrade path from opportunistic desktop monitoring to an always-on Linux hub and dedicated sensors; and
- local-first: core operation does not require a Cozy SOC account, cloud AI, or telemetry.

### Cozy SOC is not

- a guarantee of whole-home packet visibility from an ordinary desktop connection;
- a replacement for the router, switch, access point, endpoint OS, or every upstream security engine;
- an antivirus/EDR engine built from scratch;
- an offensive Wi-Fi or exploitation toolkit;
- a cloud-managed consumer appliance that stops working offline; or
- a dashboard that marks every installed engine green and calls the home protected.

## Primary journeys

Each journey includes the partial/failure state as part of the product rather than treating it as an exceptional support case.

### 1. Install and first run

**Happy path**

1. User installs Cozy SOC Desktop.
2. The package registers the controller through the supported platform service mechanism.
3. The desktop shell connects to the independently running controller.
4. The app explains local data handling and the current implementation/support status.
5. The user proceeds without creating a cloud account.

**Partial/failure states**

- service registration denied → UI remains usable enough to explain what permission is missing and how that limits operation;
- controller unavailable/version mismatch → UI reconnects or reports the mismatch; it does not spawn duplicate controllers;
- machine sleeps → the history records an observation gap; the app does not imply monitoring continued.

### 2. Enroll a home network and discover devices

**Happy path**

1. Cozy SOC identifies the current interface/network candidate.
2. The user explicitly enrolls the network as authorized scope.
3. Device Watch collects available neighbor/service observations and bounded approved discovery signals.
4. Device identity claims flow through the normalized model.
5. The user can label or correct devices.

**Partial/failure states**

- VPN, work, guest, public, or unknown network → no silent active probing;
- client isolation/VLAN boundaries → visible gap is explained;
- private/randomized addresses → identity confidence remains limited rather than manufacturing permanence.

### 3. Understand coverage

The UI answers **what source is observing what scope, how recently, and with which known gaps**.

Examples:

- Device discovery: active on the current LAN segment.
- DNS observation: not configured, or limited to clients actually using the observed resolver.
- Traffic monitoring: this computer only, or a named mirrored/gateway path when verified.
- Wireless monitoring: unavailable, or a named radio with observed bands/channels/dwell.

A zero-alert state is displayed separately from coverage and sensor health.

### 4. Enable a capability

1. User selects an outcome such as Device Watch, DNS Protection, Traffic Watch, or Wireless Watch.
2. Cozy SOC runs preflight checks: platform, permissions, hardware/data prerequisites, conflicts, resource/storage requirements, and ownership.
3. Existing installations can be connected read-only without takeover.
4. Managed installations declare the exact service/artifact and requested privileges.
5. Setup is complete only after health **and expected observation** are verified.

Failure to verify data produces `configured / unverified`, not `protected`.

### 5. Investigate a finding

1. A detector creates a Finding from observations and states confidence/limitations.
2. Related findings may correlate into one Incident with explicit correlation reasons.
3. The UI shows what was observed, why it might matter, what remains uncertain, and a safe next step.
4. Advanced users can inspect source evidence or open the relevant upstream tool when a safe deep link exists.
5. Any response action is a separate object requiring an available enforcement capability and explicit authorization.

An unusual upload is not labeled confirmed exfiltration without evidence that supports that claim.

### 6. Move monitoring to an always-on hub

1. User prepares a supported Linux host.
2. Desktop pairs with the hub through the future secure enrollment flow.
3. Compatibility, storage, and capabilities are verified.
4. Configuration/device labels and explicitly selected history move through a migration transaction.
5. Credentials that should not be cloned are reauthorized separately.
6. Authority transfers only after verification; the previous controller is not deleted first.

The result is the same Cozy SOC product and domain model running in a different deployment mode, not a second management plane.

## First vertical slice

The first implementation slice should prove the real boundaries without requiring the full security landscape:

**desktop shell → independently running controller → bounded local store → enrolled network → Device Watch observations → coverage state → device list/detail UI → sleep/reconnect gap handling**

This slice intentionally does not require router writes, Docker, Suricata, Kismet, remote sensors, or a cloud service. It exercises the contracts later capabilities need without building a speculative plugin platform first.
