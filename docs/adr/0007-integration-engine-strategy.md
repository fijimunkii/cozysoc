# ADR 0007: Minimize the default specialist-engine stack

- **Status:** Accepted
- **Date:** 2026-09-08
- **Issue:** #6
- **Evidence:** [`../integrations/evaluation-2026-09-08.md`](../integrations/evaluation-2026-09-08.md)

## Context

Cozy SOC's value is not the number of security tools it can launch. Its value is unified household device identity, honest coverage, correlated evidence, understandable findings, safe setup, and reliable maintenance.

The project can technically integrate many established tools: NetAlertX, AdGuard Home, Suricata, Zeek, RITA, Kismet, OpenCanary, Wazuh, Nmap, Npcap, and router APIs. Requiring all of them would create a large privilege, storage, update, packaging, licensing, and support surface before the product proves that each engine contributes unique value.

The Foundation architecture also requires:

- the base desktop to remain useful without Docker or an always-on hub;
- engines to remain untrusted data producers rather than control-plane peers;
- external installations to retain their ownership;
- managed artifacts to have explicit provenance, lifecycle, resource, and distribution contracts; and
- coverage to be derived from verified observations, never from process existence.

## Decision

### 1. v0.1 has no mandatory third-party security engine

Implement Device Watch with a small first-party discovery adapter and the first-party domain/coverage model. Do not make NetAlertX, Nmap, a DNS service, an IDS, or a SIEM a prerequisite for the desktop alpha.

This keeps the first vertical slice small and proves Cozy SOC's actual differentiation before specialist engines are introduced.

### 2. Add engines only when they create a distinct observation capability

The selected progression is:

- **v0.2 DNS:** AdGuard Home, external/read-only first. Managed DNS is a separately gated later slice.
- **v0.2 router:** OPNsense as the first read-only router/API reference.
- **v0.3 traffic:** Suricata as the initial Linux traffic IDS/telemetry engine, integrated through EVE JSON.
- **v0.4 wireless:** Kismet as the initial Linux monitor-mode/wireless engine on exact validated adapter/driver combinations.
- **v0.5 tripwire:** OpenCanary remains an optional managed/external candidate after isolation tests.
- **v0.5 endpoint:** Wazuh is external/read-only initially; no default managed enterprise stack.
- **v0.5 posture:** Nmap may be used only as a user-supplied executable unless redistribution rights are separately resolved.

### 3. Defer overlapping/heavy engines until measured need exists

- **NetAlertX:** do not use as the v0.1 inventory foundation. Consider an existing-instance read-only adapter later.
- **Zeek:** add only if #21 demonstrates material incremental evidence/detection value over Suricata telemetry relative to added parser/resource/update cost.
- **RITA:** do not make it a default dependency. Start with small explainable detectors over Cozy SOC-normalized telemetry and compare later if needed.

### 4. Ownership is explicit and independent of transport

Every integration declares one of these operational modes:

- `external`: user owns lifecycle/configuration;
- `managed`: Cozy SOC owns the tested deployment lifecycle; or
- `user-supplied`: Cozy SOC invokes a separately installed/licensed executable but does not distribute it.

Connecting an external service never implies permission to restart, upgrade, rewrite, or uninstall it. `managed` does not necessarily mean embedded in the desktop application; separate packages/services are preferred when appropriate.

### 5. Managed third-party artifacts are release-gated independently

Before a capability becomes managed, its issue must record the exact artifacts Cozy SOC distributes or causes to be installed, including engine, dependencies, drivers, rule/feed/data artifacts, license/notices, authoritative source, integrity/signature path, privileges/listeners, update/rollback, resource budget, and clean uninstall ownership.

The first-party MIT license does not supersede third-party terms.

### 6. APIs/events are preferred over internal storage coupling

Use supported machine boundaries:

- AdGuard Home documented control API;
- OPNsense documented API;
- Suricata EVE JSON;
- Kismet supported datasource/API contracts; and
- supported APIs for any future NetAlertX/Wazuh adapter.

Do not build core product behavior on another tool's internal database schema when a supported API/event boundary exists.

### 7. Cozy SOC owns the consumer evidence experience

Headless engines do not need to supply a GUI. Cozy SOC provides normalized evidence views and incident correlation. Upstream admin UIs may be exposed through safe optional deep links, but are never the primary household experience or an authorization boundary.

## Consequences

### Positive

- v0.1 remains small, local-first, and useful without infrastructure changes.
- Each new engine maps to a clear user-visible capability.
- Fewer privileged parsers/services run by default.
- Engine replacement is possible because the controller owns normalized contracts and identity.
- Licensing/provenance work is attached to actual distributed artifacts instead of guessed globally.
- Existing home-lab installations are first-class rather than overwritten by Cozy SOC.

### Costs

- Cozy SOC must implement a modest native discovery adapter rather than delegating all inventory to NetAlertX.
- Cozy SOC must build its own evidence UI for headless engines such as Suricata.
- External and managed modes need separate lifecycle/testing paths.
- Optional integrations may initially require more user setup than an indiscriminate all-in-one image.

## Rejected alternatives

### Bundle a complete security distribution

Rejected. A default Suricata + Zeek + RITA + inventory + DNS + endpoint stack would create unnecessary resource, privilege, storage, packaging, and maintenance burden for a household product.

### Use NetAlertX as the mandatory device database

Rejected for v0.1. Its broad scanner/plugin stack and deployment requirements are useful for its own product but would make Cozy SOC's most fundamental identity path depend on an external runtime.

### Require Zeek and RITA for v0.3

Rejected until detector evidence demands them. Suricata EVE should be evaluated first.

### Bundle Nmap/Npcap by default

Rejected under the current distribution model. Their upstream redistribution terms require separate treatment; the initial posture-assessment design may use a user-supplied Nmap installation instead.

## Validation and change control

This ADR selects **which role each candidate may play**, not an evergreen supported-version list. An implementation must still pass the exact version/platform/security/hardware gates in its own roadmap issue.

Changing a deferred engine to mandatory, changing an integration from external to managed, or adding a new distributed binary/feed/driver requires an explicit update to the integration evaluation and applicable security/release review.
