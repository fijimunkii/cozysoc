# Architecture validation gates

Architecture documentation is not proof that the proposed platform and integration paths work. This file maps the remaining P0 evidence needed to close issue #3.

## Issue #4 — threat model

The threat model must evaluate at least these boundaries from the system model:

- React renderer → desktop shell;
- desktop shell → local controller IPC;
- controller → privileged helper;
- controller → local data store;
- controller → external/managed integration;
- remote sensor → controller;
- future browser origin → headless controller;
- update/package artifacts → installed binaries; and
- user-approved network scope → active discovery or write actions.

### Stop/reconsider conditions

Revisit the architecture if #4 shows that:

- the selected shell cannot keep generic process/system authority out of renderer reach;
- local control cannot be authenticated/permissioned without opening a broader attack surface;
- the controller requires routine root/admin execution instead of narrow helper operations;
- a managed-engine design requires an unrestricted container/socket boundary; or
- remote-sensor compromise can become controller/router authority through the proposed contract.

## Issue #5 — real feasibility experiments

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

### Stop/reconsider conditions

Revisit a Proposed ADR if the reference platform cannot meet independent service lifetime, security boundary, or resource targets without substantially increasing privilege/complexity.

## Issue #6 — integration and redistribution evaluation

Validate candidate engines separately rather than allowing the architecture to imply a mandatory stack.

For each selected candidate, establish:

- maintained version and compatible architecture;
- API/event contract;
- required privileges and observation prerequisites;
- managed versus external ownership semantics;
- resource/retention cost;
- deep-link/evidence-view behavior;
- update/security maintenance path; and
- license/redistribution obligations for binaries, drivers, feeds, rules, and bundled assets.

### Stop/reconsider conditions

Do not bundle or manage an engine when its license, update provenance, privilege model, footprint, or failure behavior cannot meet the relevant architecture/product constraints. Prefer connect-to-existing or defer the capability.

## Proposed ADR promotion checklist

A Proposed architecture ADR becomes Accepted only when:

1. its named P0 blockers have recorded evidence;
2. failure cases have been tested, not just the happy path;
3. the support matrix is updated with exact tested platform/hardware versions;
4. resource measurements are recorded rather than assumed;
5. threat-model requirements are reflected in the decision; and
6. no unresolved high-impact issue is hidden behind a support claim.

## Closure rule for issue #3

Issue #3 remains open after this architecture-baseline PR. Close it only after #4–#6 have produced enough evidence to either:

- accept the Proposed shell/platform/packaging decisions; or
- revise them and merge the superseding architecture changes.
