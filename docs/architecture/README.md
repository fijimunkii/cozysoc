# Cozy SOC architecture

This directory describes the **current architecture contract** for Cozy SOC. Architectural decision records in [`../adr/`](../adr/) explain why significant choices were made and which choices remain provisional.

The canonical roadmap remains [issue #1](https://github.com/fijimunkii/cozysoc/issues/1). This architecture baseline advances [issue #3](https://github.com/fijimunkii/cozysoc/issues/3); it does **not** close it. Hardware, platform, threat-model, and integration assumptions still require the evidence defined in issues [#4](https://github.com/fijimunkii/cozysoc/issues/4), [#5](https://github.com/fijimunkii/cozysoc/issues/5), and [#6](https://github.com/fijimunkii/cozysoc/issues/6).

## Documents

- [Product boundaries and journeys](product-boundaries.md) — what the product is, what it is not, and the primary user journeys.
- [System model](system-model.md) — processes, trust boundaries, lifecycle ownership, API boundaries, and deployment modes.
- [Domain model](domain-model.md) — the shared vocabulary for networks, sensors, observations, devices, findings, incidents, coverage, and actions.
- [Support and capability matrix](support-matrix.md) — candidate platforms and the prerequisites/blind spots for each monitoring capability.
- [Resource budgets](resource-budgets.md) — measurable targets for the core app before optional security engines are added.
- [Validation gates](validation-gates.md) — what #4–#6 must prove before provisional decisions become support claims.

## Decision status

ADRs use these states:

- **Accepted** — the architectural choice is the current implementation contract. It may still have tests and release gates.
- **Proposed** — the preferred direction, but a named P0 validation result can still change it without a migration commitment.
- **Superseded** — replaced by a later ADR.
- **Rejected** — considered and intentionally not selected.

Merging a Proposed ADR does not mean that its platform, framework, or hardware path is supported.

## Core architecture invariants

These do not depend on the result of a particular USB adapter or packet-capture experiment:

1. **The controller is independent of the desktop window.** Closing or crashing the UI is not a controller stop signal.
2. **The renderer is not privileged.** It never receives generic shell, service-manager, capture, router, database, or container authority.
3. **The controller is the Cozy SOC authority.** It owns Cozy SOC configuration, normalized state, capability state, and the local data store.
4. **External services retain ownership by default.** Connecting an existing service does not silently make Cozy SOC its lifecycle/configuration owner.
5. **Desired state, process health, verified observation, and enforcement authority are distinct.** A running process is not proof of coverage.
6. **Sensors are observation points, not control-plane peers.** Sensor input is authenticated where remote, bounded, and treated as untrusted data.
7. **Platform privilege is narrow.** Operations that genuinely need elevation go through small allowlisted helpers or platform service mechanisms.
8. **Desktop mode remains useful by itself.** An always-on hub increases continuity and observation options; it is not required to discover the local network or use the app.
9. **One core service supports desktop and hub deployment modes.** Platform adapters vary; the domain model does not fork into separate products.
10. **No arbitrary plugin execution.** Integrations are curated adapters with declared capabilities and ownership.

## Explicit architecture non-goals

The initial architecture does not include:

- Kubernetes or a household microservice platform;
- an arbitrary plugin marketplace or user-supplied executable manifests;
- direct renderer access to Docker, root/admin APIs, packet capture, the service manager, router credentials, or the database file;
- a GUI-owned background controller;
- an unauthenticated blanket localhost management listener;
- mandatory cloud identity, AI, or telemetry;
- automatic network mutation as a prerequisite for useful operation; or
- a single global score that conflates security, monitoring coverage, and network quality.

Any future exception needs a new ADR with a demonstrated requirement and security review.
