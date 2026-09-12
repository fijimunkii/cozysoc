# Cozy SOC

**Serious security. Right at home.**

Cozy SOC is a friendly, local-first home network security hub. The goal is to make useful network visibility and security capabilities approachable without requiring someone to become a security analyst or network administrator first.

> **Status:** Active v0.1 development, with a working local controller, CLI and browser UI. There is no released desktop alpha or supported installer yet. Developer and lab evidence does not establish whole-home protection, packaged platform support, or continuous service operation.

## Product direction

Cozy SOC is designed around a desktop application backed by an independently running local service. The same interface should later be able to manage an always-on Linux hub and optional network or wireless sensors.

The product should help a household answer three questions:

1. **What is connected?**
2. **What looks unusual?**
3. **What should I do next?**

Planned capability areas include device discovery and presence, DNS protection, traffic analysis, explainable suspicious-activity detection, network quality checks, optional wireless monitoring with tested USB adapters, and integrations with specialist open-source security tools.

## What works in the developer build

- One `cozysoc` executable provides independent controller (`serve`), authenticated CLI, local browser (`web`), and development orchestration (`dev`) modes.
- Controller-owned SQLite stores bounded local evidence and settings behind an authenticated Unix-socket API. The browser has a separate session and narrowly typed endpoints.
- Network enrollment, explicit Device Watch enablement, device/evidence views, and coverage reporting distinguish observed data from missing or stale coverage.
- Experimental macOS gateway and selected-resolver checks require controller opt-in and interactive one-shot approval. Retained history and plain-language diagnosis preserve evidence age and uncertainty.
- HTTPS settings and reviews disclose the exact request, TLS policy, traffic limits and privacy impact. The controller owns audited one-shot run control, and the foreground macOS `https-check` client requires explicit approval. macOS controllers started with `--experimental-https-checks` expose the native one-shot protocol. Isolated native tests cover trust rejection, an owned 204 response, terminal approval/decline/expiry, cooldown and persisted audits; ordinary controllers leave execution disabled. Read-only `https-history` retains status, timing and incomplete-audit evidence without private endpoints or requests.

See [frontend and local web](docs/development/frontend.md), [Device Watch](docs/development/device-watch.md), [gateway checks](docs/development/interactive-gateway-check.md), [resolver checks](docs/development/resolver-consent.md), [retained diagnosis](docs/development/retained-quality-diagnosis.md), and [HTTPS review](docs/development/https-controller.md) and [HTTPS history](docs/development/https-history.md).

## Run the developer UI

From the repository root, with Go 1.27.1 and Node 24.20.x (the versions pinned by this repository):

```sh
npm --prefix ui ci --ignore-scripts --no-audit --no-fund
npm --prefix ui run build
go run ./cmd/cozysoc dev
```

Open the authenticated loopback URL printed by the command. `dev` can start a temporary controller and stops only the controller it owns when it exits. Use separate `serve` and `web` processes when exercising independent lifetimes; this is not an installer or proof of reboot/sleep behavior. Starting the UI does not enable Device Watch or authorize active checks. See the [command guide](docs/development/unified-cli.md) and [controller guide](docs/development/controller.md) for explicit enrollment and capability commands.

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

These are not current support claims. Issues #5/#56 must prove real service lifetime, packet visibility, USB Wi-Fi behavior, packaging, permissions, sleep/restart behavior, and resource budgets; issue #6 validates engine compatibility and redistribution terms.

## Roadmap

The canonical roadmap is [GitHub issue #1](https://github.com/fijimunkii/cozysoc/issues/1).

Foundation architecture, threat-model and integration decisions, the controller/IPC/storage baseline, and unified entrypoints have landed. Current implementation work is in v0.1, especially network quality (#14), with capability, Device Watch, coverage and frontend work (#9, #11–#13) still open.

Release gates remain open: [hardware evidence #56](https://github.com/fijimunkii/cozysoc/issues/56), [installers and updates #28](https://github.com/fijimunkii/cozysoc/issues/28), [lab and performance gates #29](https://github.com/fijimunkii/cozysoc/issues/29), and [privacy/recovery #30](https://github.com/fijimunkii/cozysoc/issues/30). CI exercises macOS 15/26 native labs and Linux controller/frontend/process checks; those scoped results do not promote every architecture candidate to a supported platform.

The first software release line is **v0.1**, the desktop alpha. Later roadmap stages use v0.2–v0.5, with v1.0 reserved for the first broadly ready release.

## Security and privacy

Cozy SOC will process unusually sensitive household data, potentially including device identities, network metadata, DNS activity, and security findings. The project therefore treats least privilege, bounded retention, safe diagnostics, authenticated control paths, and explicit coverage limitations as product requirements rather than post-release hardening.

Please read [SECURITY.md](SECURITY.md) before reporting a vulnerability. Do not post sensitive household, credential, packet-capture, or vulnerability details in a public issue.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Changes should be tied to a scoped GitHub issue, use conventional commit subjects, and keep implementation claims aligned with tested behavior.

## License

Cozy SOC's first-party code and documentation are licensed under the [MIT License](LICENSE). The decision and alternatives are documented in [ADR 0001](docs/adr/0001-license.md).

Third-party engines, rules, drivers, data feeds, and bundled artifacts remain subject to their own licenses and distribution terms. Inclusion in the roadmap does not imply that Cozy SOC may redistribute a component.
