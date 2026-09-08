# Cozy SOC

**Serious security. Right at home.**

Cozy SOC is a friendly, local-first home network security hub. The goal is to make useful network visibility and security capabilities approachable without requiring someone to become a security analyst or network administrator first.

> **Status:** planning and repository bootstrap. Cozy SOC does not have a released or functional build yet. Capabilities described below are roadmap targets, not current protection claims.

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

## Working architecture direction

The current roadmap is evaluating:

- a **Go** controller/background service;
- a shared **TypeScript + React** interface;
- **Tauri** as a possible desktop shell;
- bounded local storage;
- narrowly scoped native helpers where elevated privileges are genuinely required; and
- **Linux** as the reference platform for advanced packet and wireless sensors.

These are working directions, not finalized architecture decisions. The P0 feasibility work will validate service lifetime, packet visibility, USB Wi-Fi support, packaging constraints, resource use, and integration licenses before the stack is locked.

## Roadmap

The canonical roadmap is [GitHub issue #1](https://github.com/fijimunkii/cozysoc/issues/1).

The first implementation task is [issue #2](https://github.com/fijimunkii/cozysoc/issues/2), which establishes repository conventions, documentation, and CI before product code is introduced.

## Security and privacy

Cozy SOC will process unusually sensitive household data, potentially including device identities, network metadata, DNS activity, and security findings. The project therefore treats least privilege, bounded retention, safe diagnostics, authenticated control paths, and explicit coverage limitations as product requirements rather than post-release hardening.

Please read [SECURITY.md](SECURITY.md) before reporting a vulnerability. Do not post sensitive household, credential, packet-capture, or vulnerability details in a public issue.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Changes should be tied to a scoped GitHub issue, use conventional commit subjects, and keep implementation claims aligned with tested behavior.

## License

Cozy SOC's first-party code and documentation are licensed under the [MIT License](LICENSE). The decision and alternatives are documented in [ADR 0001](docs/adr/0001-license.md).

Third-party engines, rules, drivers, data feeds, and bundled artifacts remain subject to their own licenses and distribution terms. Inclusion in the roadmap does not imply that Cozy SOC may redistribute a component.