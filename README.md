# Cozy SOC

**Serious security. Right at home.**

Cozy SOC is a friendly, local-first home network security hub. The goal is to make useful network visibility and security capabilities approachable without requiring someone to become a security analyst or network administrator first.

> **Status:** Foundation architecture and feasibility. Cozy SOC does not have a released or functional build yet. Capabilities described below are roadmap targets, not current protection claims.

## Product direction

Cozy SOC is designed around a desktop application backed by an independently running local service. The same interface should later be able to manage an always-on Linux hub and optional network or wireless sensors.

The product should help a household answer three questions:

1. **What is connected?**
2. **What looks unusual?**
3. **What should I do next?**

Planned capability areas include device discovery and presence, DNS protection, traffic analysis, explainable suspicious-activity detection, network quality checks, optional wireless monitoring with tested USB adapters, and integrations with specialist open-source security tools.

## A core design rule: coverage must be honest

Discovery is not the same as traffic visibility, and a running sensor is not proof that it can see everything.

Cozy SOC will distinguish:

- what has been configured;
- what has actually been observed;
- what is currently healthy and fresh;
- what is limited or unverified; and
- what is outside the current observation point.

The interface should never turn "zero alerts" into an unqualified "fully protected" claim.

## Principles

- **Local-first.** No account, cloud AI, or telemetry should be required for the core product.
- **Desktop-first, not desktop-only.** Closing the UI should not stop the installed service. Continuous monitoring may require an always-on host.
- **Friendly, but precise.** Explain evidence and uncertainty without alarmist language.
- **Read-only and passive first.** Active checks need explicit authorized scope. Network changes need deliberate consent and a recovery path.
- **Integrate before reinventing.** Prefer maintained specialist tools when they fit, while presenting a unified Cozy SOC experience.
- **Existing installations are first-class.** Connecting to an existing service must not silently turn it into Cozy SOC-managed infrastructure.
- **Security, monitoring coverage, and network quality are separate concepts.**

## Architecture

The current architecture baseline is documented in [`docs/architecture/`](docs/architecture/README.md).

Accepted direction:

- a **Go** controller/background service, independent of the desktop window;
- a shared **TypeScript + React** interface;
- a controller-owned **SQLite** store with no direct frontend/engine database access;
- a modular-monolith core with narrow platform and integration adapters; and
- narrowly scoped privileged helpers only where elevation is genuinely required.

Still provisional pending Foundation validation:

- **Tauri v2** as the first thin desktop shell;
- **macOS 13+ Apple Silicon** as the first desktop reference candidate; and
- **Ubuntu Server 26.04 LTS** on tested amd64/arm64 hardware as the initial headless hub/advanced-sensor candidate.

These are not current support claims. Issue #5 must prove real service lifetime, packet visibility, USB Wi-Fi behavior, packaging, permissions, sleep/restart behavior, and resource budgets; issue #6 validates engine compatibility and redistribution terms.

## Roadmap

The canonical roadmap is [GitHub issue #1](https://github.com/fijimunkii/cozysoc/issues/1).

Repository bootstrap is complete. Current Foundation work is intentionally concentrated in:

- [#3 — architecture and product boundaries](https://github.com/fijimunkii/cozysoc/issues/3);
- [#4 — threat model](https://github.com/fijimunkii/cozysoc/issues/4);
- [#5 — real service/capture/USB feasibility experiments](https://github.com/fijimunkii/cozysoc/issues/5); and
- [#6 — integration, license, packaging, and minimum-engine evaluation](https://github.com/fijimunkii/cozysoc/issues/6).

The first software release line is **v0.1**, the desktop alpha. Later roadmap stages use v0.2–v0.5, with v1.0 reserved for the first broadly ready release.

## Security and privacy

Cozy SOC will process unusually sensitive household data, potentially including device identities, network metadata, DNS activity, and security findings. The project therefore treats least privilege, bounded retention, safe diagnostics, authenticated control paths, and explicit coverage limitations as product requirements rather than post-release hardening.

Please read [SECURITY.md](SECURITY.md) before reporting a vulnerability. Do not post sensitive household, credential, packet-capture, or vulnerability details in a public issue.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Changes should be tied to a scoped GitHub issue, use conventional commit subjects, and keep implementation claims aligned with tested behavior.

## License

Cozy SOC's first-party code and documentation are licensed under the [MIT License](LICENSE). The decision and alternatives are documented in [ADR 0001](docs/adr/0001-license.md).

Third-party engines, rules, drivers, data feeds, and bundled artifacts remain subject to their own licenses and distribution terms. Inclusion in the roadmap does not imply that Cozy SOC may redistribute a component.
