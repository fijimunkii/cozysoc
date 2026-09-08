# ADR 0002: Use an independent controller with narrow platform adapters

- **Status:** Accepted
- **Date:** 2026-09-08
- **Decision owners:** Cozy SOC maintainers
- **Issues:** #3, #4, #7, #8, #15

## Context

Cozy SOC is desktop-first, but security monitoring may need to continue after the desktop window closes and may later move to an always-on Linux host. Some operations will require platform-specific privilege while most controller work should not.

Coupling the controller to the GUI process would make window lifecycle, renderer crashes, and user logout part of the monitoring lifecycle. Running the whole controller as root/admin would make every parser/integration bug part of the privileged attack surface. Splitting every capability into a service would create operational complexity inappropriate for a household product.

## Decision

Use a **modular-monolith controller** as the single Cozy SOC authority for an installation.

The controller:

- runs independently through the platform's supported service mechanism;
- owns Cozy SOC configuration, normalized state, capability state, and local persistence;
- runs unprivileged for ordinary operation;
- delegates genuinely privileged operations to narrow platform helpers;
- consumes curated integrations through explicit adapters; and
- supports desktop and headless-hub deployment modes from the same core code/domain model.

The desktop renderer and shell are clients. Closing/crashing the UI is not a controller stop signal.

Remote sensors are scoped observation sources, not control-plane peers. External integrations retain lifecycle/configuration ownership unless the user explicitly transitions to a managed capability.

## Alternatives considered

### GUI-owned background process

Simpler packaging at first, but fails the product requirement that UI lifecycle and monitoring lifecycle be independent. It also makes reliable restart/reboot/headless operation harder to reason about.

### Entire controller as root/admin

Rejected because ordinary event parsing, storage, and UI/API work do not justify elevated authority.

### Per-engine microservices / Kubernetes-style orchestration

Rejected for the initial product. It increases deployment, update, storage, health, and security-boundary complexity without a demonstrated household requirement.

### Container socket as the management boundary

Rejected as a generic control mechanism. A capability may use a container where validated, but the renderer/controller must not gain unrestricted Docker/socket authority as an integration shortcut.

## Consequences

- Issue #7 implements the controller lifecycle and domain API.
- Issue #8 defines/authenticates local IPC and privileged helper contracts.
- Issue #9 manages capability lifecycle through curated manifests/adapters rather than arbitrary executable plugins.
- Issue #15 reuses the controller core in headless-hub mode and defines secure sensor enrollment/authority transfer.
- Platform-specific service registration and helper code remain adapters rather than leaking into the core domain.
- Integration/parser compromise still matters, but it does not automatically confer root/admin or router-write authority.

## Validation

The process model is accepted as an architecture invariant, but #4 and #5 must still verify the concrete local IPC, privilege, service registration, crash/restart, reboot, sleep/resume, and packaging implementations before release.
