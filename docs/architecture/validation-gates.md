# Architecture validation gates

Architecture documentation is not proof that the proposed platform and integration paths work. This file maps the remaining **Foundation** evidence needed to close issue #3 and finalize the implementation contract for v0.1.

## Issue #4 — threat model

**Baseline result:** [`docs/security/threat-model.md`](../security/threat-model.md) and [`docs/security/security-requirements.md`](../security/security-requirements.md).

The threat model evaluates the architecture boundaries from the system model and adds explicit requirements for:

- renderer → shell privilege minimization;
- shell/local client → controller authentication and browser-to-local defenses;
- controller → privileged helper caller/argument validation;
- controller → local data store ownership and bounded storage;
- controller → external/managed integration endpoint identity, SSRF, redirects, TLS, and credential scoping;
- remote sensor → controller authentication, scope, replay, and data-plane-only authority;
- future browser origin → headless controller authentication, TLS, origin, and CSRF controls;
- update/package/feed artifacts → installed code/rules provenance and anti-downgrade behavior;
- user-approved network scope → active discovery/assessment confinement; and
- findings → network actions as a deliberately separated authority boundary.

### Architecture consequences from #4

The baseline threat model **supports the existing architecture** rather than forcing a redesign, provided implementation preserves its narrow boundaries. In particular:

- the renderer cannot receive generic system/process authority;
- local control cannot become an ambient unauthenticated HTTP endpoint;
- the ordinary controller cannot require routine root/admin execution;
- privileged helpers must independently validate typed allowlisted operations;
- managed engines and enrolled sensors remain untrusted data producers rather than control-plane peers;
- integration credentials must be bound to an explicitly approved endpoint authority; and
- detection/finding state cannot itself authorize network mutation.

A future implementation that cannot satisfy these requirements triggers an architecture/security review rather than silently weakening the model.

## Issue #5 — real feasibility experiments

**Harness result:** [`experiments/foundation/feasibility/`](../../experiments/foundation/feasibility/). Real-machine evidence remains tracked by #56.

### Desktop service lifetime

On the first reference desktop candidate, prove:

- controller registration through the supported OS service mechanism;
- UI close/reopen does not stop or duplicate the controller;
- controller crash/restart behavior;
- reboot behavior;
- denied registration/elevation behavior;
- sleep/resume and network-interface change behavior; and
- clean unregister/uninstall.

### Desktop shell packaging

Prove the preferred shell can distribute the controller artifact without requiring runtime parent-child ownership. Measure shell/controller startup and memory separately against the resource targets.

### Packet visibility

Prove with owned lab traffic:

- host-only capture boundaries on an ordinary desktop connection;
- traffic delivered through the chosen Linux mirror/tap/gateway test arrangement;
- one-way/asymmetric/missing capture conditions; and
- capture freshness/drop signals needed by the coverage model.

### USB Wi-Fi

On real supported Linux hardware, record exact adapter USB ID, chipset/revision, firmware, kernel/driver, bands/channels, permissions, monitor-mode behavior, unplug/replug, and cleanup.

### Security evidence required by #4

The #5 experiments must additionally demonstrate that the selected desktop/service/sensor paths can meet the applicable security requirements without broad privilege shortcuts, especially SEC-002–SEC-010, SEC-022, SEC-027–SEC-029, SEC-034, and SEC-035.

### Stop/reconsider conditions

Revisit a Proposed ADR if the reference platform cannot meet independent service lifetime, security boundary, or resource targets without substantially increasing privilege/complexity.

## Issue #6 — integration and redistribution evaluation

**Baseline result:** [`docs/integrations/evaluation-2026-09-08.md`](../integrations/evaluation-2026-09-08.md) and [ADR 0007](../adr/0007-integration-engine-strategy.md).

The Foundation integration decision establishes that:

- v0.1 has no mandatory third-party engine;
- native discovery is the base inventory path;
- AdGuard Home is the v0.2 DNS candidate, external/read-only first;
- OPNsense is the first v0.2 read-only router reference;
- Suricata EVE is the first v0.3 traffic IDS/telemetry boundary;
- Kismet is the v0.4 wireless engine on exact validated Linux radio/driver combinations;
- Zeek, RITA, and managed NetAlertX are deferred until measured incremental value justifies their footprint;
- OpenCanary, Wazuh, and Nmap remain optional v0.5 paths with deliberately constrained ownership; and
- Nmap/Npcap are not silently redistributed under the ordinary base product distribution.

### Integration contract carried into implementation

Every implemented integration must still establish:

- exact tested version and compatible architecture;
- supported API/event contract rather than internal-database coupling;
- required privileges and observation prerequisites;
- managed, external, or user-supplied ownership semantics;
- resource/retention cost;
- deep-link versus Cozy SOC-native evidence-view behavior;
- update/security maintenance and artifact provenance path;
- license/distribution review for the exact binaries, drivers, feeds, rules, and assets actually shipped or installed; and
- uninstall/recovery ownership boundaries.

### Security evidence required by #4

Each implementation must document how it satisfies or constrains applicable requirements for hostile input, endpoint identity/SSRF, TLS, external ownership, secret handling, artifact provenance, resource bounds, and untrusted-engine isolation, notably SEC-007–SEC-008, SEC-011–SEC-014, SEC-019, SEC-022, and SEC-028–SEC-029.

### Stop/reconsider conditions

Do not bundle or manage an engine when its license/distribution path, update provenance, privilege model, footprint, endpoint behavior, or failure behavior cannot meet the relevant architecture/security constraints. Prefer external/read-only, user-supplied, or deferred capability instead.

## Proposed ADR promotion checklist

A Proposed architecture ADR becomes Accepted only when:

1. its named Foundation blockers have recorded evidence;
2. failure cases have been tested, not just the happy path;
3. the support matrix is updated with exact tested platform/hardware versions;
4. resource measurements are recorded rather than assumed;
5. applicable threat-model requirements have concrete implementation/test evidence; and
6. no unresolved high-impact issue is hidden behind a support claim.

## Closure rule for issue #3

After the integration-strategy baseline is merged, issue #3 remains open only for the release-driving real-machine evidence in #5/#56 needed to accept or revise the Proposed shell/reference-platform decisions.

Close #3 only when that evidence is sufficient to either:

- accept the Proposed shell/platform/packaging decisions; or
- revise them and merge the superseding architecture changes.

Closing #3 marks the Foundation architecture/security/integration contract as ready for v0.1 implementation; it is not itself a software release.
