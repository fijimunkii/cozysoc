# Product roadmap

This document is the canonical product roadmap. The [README](../README.md)
describes the current developer build; the [documentation index](README.md) links
its architecture, behavior and operating contracts. [Issue #1](https://github.com/fijimunkii/cozysoc/issues/1)
coordinates scoped work. Issue status and merged PRs do not establish a release
or a supported capability.

## Product outcome

Cozy SOC is a friendly, local-first home-network security hub: a desktop interface
backed by an independently running service, with an upgrade path to always-on Linux
hubs and optional sensors. It helps people know what is connected, notice unusual
activity and understand what to do next.

The product combines guided setup, shared device identity, correlated evidence,
understandable findings, safe operation and reliable maintenance. It is not a
dashboard launcher or a mandatory bundle of every security engine. Advanced users
retain access to upstream tools.

## Current release status

The project is in v0.1 development. A developer controller, CLI and browser UI are
available; there is no released desktop alpha or supported installer. Physical
hardware, installed service lifetime, permissions, sleep/reboot, storage/resource
budgets and release recovery controls require further validation. See the
[README](../README.md) for available behavior and the [support matrix](architecture/support-matrix.md)
for candidate platforms and capability prerequisites.

## Product invariants

- Closing the UI must not stop an installed controller service. A sleeping desktop
  cannot provide continuous monitoring; an always-on host is an optional deployment.
- One user-facing `cozysoc` executable provides explicit controller, CLI, web and
  development modes. One executable does not imply one process lifetime.
- Discovery, observation, detection and enforcement are distinct. Show sources,
  evidence periods and gaps; running processes and zero alerts do not prove protection.
- Core storage and processing are local. No account, cloud AI or telemetry is
  required, and full packets are not retained by default.
- Prefer maintained specialist tools and support both existing installations and
  tested managed deployments. Connecting an existing service does not transfer ownership.
- Capability enablement requires verified prerequisites and operation. Unsupported
  hardware or missing visibility must remain explicit.
- Read-only and passive operation come first. Active checks require authorized
  scope. Network changes require deliberate consent, verification and tested recovery.
- Security, monitoring coverage and network quality remain separate in the interface.
- Claims must be precise: unusual uploads do not prove exfiltration, and unfamiliar
  wireless networks do not prove malicious access points.

## Release progression

These are intended product stages, not promised dates. Foundation is validation
work, not a product release. `P0/P1/P2` denote priority when used, not release phases.

| Stage | Required product scope |
| --- | --- |
| Foundation | Architecture and reference-platform decisions; threat model; real feasibility evidence; integration, ownership and redistribution decisions. Resolve high-risk assumptions before committing to them. |
| v0.1 — Desktop alpha | Independent controller and authenticated local API; protected secrets and narrow privilege; capability catalog and managed/external lifecycle; normalized evidence and temporal identity; bounded storage; Device Watch and presence; coverage/health; shared frontend and onboarding; network-quality checks and diagnosis. |
| v0.2 — Always-on home security | Secure Linux hub/sensor enrollment and desktop-to-hub migration; existing-instance AdGuard Home observation and separately gated managed DNS; one supported read-only router adapter; correlated incidents and low-noise notifications. |
| v0.3 — Traffic Watch | Validated Linux traffic sensor and Suricata EVE integration; curated rule feeds with safe updates/rollback; explainable upload, destination, beaconing and scanning anomalies. Zeek/RITA require demonstrated incremental value. |
| v0.4 — Wireless Watch | Kismet integration and guided setup on tested USB Wi-Fi hardware; home-network enrollment; wireless findings and honest radio-quality/coverage views. |
| v0.5 — Optional capabilities | Isolated OpenCanary tripwires; conservative service-exposure/posture assessments; optional Wazuh endpoint visibility; explicitly authorized and reversible network enforcement. Promote capabilities individually. |
| v1.0 | A broadly ready release with its gate defined from measured reliability, support burden, compatibility, privacy/security validation and real v0.x use. The final gate is not yet specified. |

Read-only DNS/router integration does not require automatic router configuration.
Managed DNS changes remain separately gated. Wireless Watch can proceed when its
Foundation and shared dependencies are ready without waiting for every Traffic
Watch detector. Optional v0.5 capabilities are not prerequisites for earlier releases.

## Release gates

### v0.1 desktop alpha

All v0.1 scope must work end-to-end on the selected reference desktop OS, including
applicable installation/update, privacy/recovery and lab requirements below.
Foundation decisions must support the chosen implementation and observation paths.

An installed `cozysoc serve` service must survive UI closure and reboot.
`cozysoc dev` is not evidence of this production lifecycle. Denied permissions,
sleep/resume, offline operation and blind spots must be understandable. Scoped
discovery and network-quality checks must work without router changes, mandatory
Docker or an account. Developer-only and unsigned builds must be clearly marked.

### v0.2

Meet v0.1 requirements and provide a secure always-on hub path, existing-instance
DNS observation, a supported read-only router integration and correlated incidents.
Managed DNS configuration requires successful rollback tests.

### v0.3

Provide the reference traffic sensor, maintained rule lifecycle and initial
explainable behavioral detectors. Release artifacts must be signed and tested,
with applicable privacy, recovery and lab gates satisfied.

### v0.4

Wireless Watch must pass on at least one documented chipset/driver/OS combination
with real hardware evidence. Other combinations remain unsupported until validated.
One radio must never be presented as continuous all-channel observation.

### v0.5 and v1.0

Each optional capability must pass its security, compatibility, noise and resource
gates independently. Define the v1.0 gate from measured operating evidence rather
than treating completion of an implementation checklist as broad readiness.

### Requirements across releases

- Tested installers, authenticated updates, provenance and safe uninstall.
- Security, coverage, noise and performance validation, including failure paths.
- Privacy controls, retention, backup/restore and redacted diagnostics.
- Resource budgets measured on named hardware and settings, overload recovery,
  and an at-least-24-hour sustained run before beta.
- Explicit pass/fail/untested status with evidence for each applicable gate.
  Hardware claims require actual hardware tests; fixture or CI success is insufficient.

See [resource budgets](architecture/resource-budgets.md),
[validation gates](architecture/validation-gates.md),
[security requirements](security/security-requirements.md) and
[testing](development/testing.md) for their contracts and evidence boundaries.

## Architecture and authority boundaries

The accepted and provisional decisions are defined by the
[architecture contract](architecture/README.md) and its ADRs. The core is a modular
monolith with a curated capability catalog, not a microservice platform or arbitrary
plugin marketplace. The shared TypeScript/React interface and optional desktop
shell do not own controller lifetime. There is no cross-platform feature-parity promise.

`cozysoc serve` is service-manager-owned in a production installation. CLI commands
use protected, authenticated IPC. `cozysoc web` exposes only typed, allowlisted
browser operations. Loopback development binding does not establish a hardened
remote-browser deployment. `cozysoc dev` is a development convenience only.

The browser must not receive controller session secrets, a generic RPC proxy,
arbitrary process execution or unrestricted filesystem/network authority. State
changes require authentication and the documented origin, Host and CSRF protections.
Any desktop shell must preserve these lifecycle and authority boundaries.

Integration candidates include NetAlertX, AdGuard Home, Suricata, Zeek/RITA, Kismet,
OpenCanary, Nmap, Wazuh and OPNsense. Candidacy is not a commitment to bundle or
redistribute a component. Supported versions, ownership modes and distribution
paths require the [integration policy](integrations/README.md) and relevant evidence.

## Explicit exclusions

Custom antivirus; exploitation/password guessing; ARP-spoofing interception;
Wi-Fi deauthentication or injection; TLS interception; autonomous blocking;
mandatory AI/cloud services; hosted SaaS/multi-tenancy; a full SIEM stack by default;
and support claims for every router or adapter are outside this roadmap.
Domain and trademark availability are not established by the product name.

## Maintaining this roadmap

Change requirements here and update the affected behavior/support documents when
the product contract changes. Keep current behavior, planned requirements and
validated support clearly distinguished. Use issues and PRs for execution status
and implementation discussion. Keep dated test results and ADR rationale as
supporting evidence, not as a running implementation narrative in feature guides.
