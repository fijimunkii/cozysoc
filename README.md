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
- The local Findings view shows retained informational Device Watch arrivals and supports an audited "reviewed" mark without treating inferred identity as a security verdict.
- Scoped Device Watch merge and observation-split controls in the native CLI and local browser can correct duplicate or wrongly grouped identities without rewriting retained observations; evidence detail discloses the original association and each correction can be undone.
- Experimental macOS gateway and selected-resolver checks require controller opt-in and interactive one-shot approval. DNS and HTTPS targets can be saved or retired in the local browser or native command; saving sends no traffic or consent. Selected gateway, resolver and HTTPS checks can be reviewed and approved separately in the local browser or native command. Retained history and plain-language diagnosis preserve evidence age and uncertainty.
- Experimental HTTPS checks require `--experimental-https-checks` and explicit one-shot approval through `https-check` or the authenticated local browser. Settings and review disclose the request, TLS policy, traffic limits and privacy impact. Read-only CLI and browser history preserve original status, timing and missing-audit evidence; refreshing saved evidence never sends a probe.
- Retained diagnosis compares the latest available gateway, resolver and HTTPS runs from one bounded snapshot. Native and browser views preserve HTTP expectations, failure stages and original times; mismatched context, stale samples and newer unknowns prevent comparison. At least two layers are required, and no conclusion declares the internet up or down.
- A candidate native OPNsense 26.7.4 connection can verify router version over a protected external API link. Separate foreground CLI or local-browser review and approval can read bounded ARP/NDP tables once and keep only in-scope router-reported neighbor evidence. This does not establish device identity, whole-network visibility, or router support.

See [frontend and local web](docs/development/frontend.md), [Device Watch](docs/development/device-watch.md), [gateway checks](docs/development/interactive-gateway-check.md), [resolver checks](docs/development/resolver-consent.md), [retained diagnosis](docs/development/retained-quality-diagnosis.md), and [HTTPS review](docs/development/https-controller.md) and [HTTPS history](docs/development/https-history.md).

## Run the developer UI

From the repository root, with Go 1.27.1 and Node 24.20.x (the versions pinned by this repository):

```sh
npm --prefix ui ci --ignore-scripts --no-audit --no-fund
npm --prefix ui run build
go run ./cmd/cozysoc dev
```

Open the authenticated loopback URL printed by the command. `dev` can start a temporary controller and stops only the controller it owns when it exits. Use separate `serve` and `web` processes when exercising independent lifetimes; this is not an installer or proof of reboot/sleep behavior. Starting the UI does not enable Device Watch or authorize active checks. See the [command guide](docs/development/unified-cli.md) and [controller guide](docs/development/controller.md) for explicit enrollment and capability commands.

To build a relocatable **unsigned developer bundle**, run `bash scripts/build-dev-bundle.sh /path/to/new/bundle`. The destination must not already exist. It contains `cozysoc`, the built `ui/dist/` assets, source/platform metadata, the first-party license, a CycloneDX `SBOM.json` of the Go standard library, embedded Go modules and browser runtime packages, their `THIRD-PARTY-NOTICES.txt`, and a versioned `CHECKSUMS.json` inventory. Run `python3 scripts/dev-bundle-checksums.py verify /path/to/new/bundle` to detect changed, missing, or unexpected files. The inventory is not signed and does not authenticate the publisher. Run `/path/to/new/bundle/cozysoc web` from any working directory to connect to an independently running controller; `--ui-dir` still selects a specific asset directory. This bundle is for local development and does not register a service or establish platform release support.

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

The [documentation](docs/README.md) is the canonical product reference. The [architecture contract](docs/architecture/README.md) defines system boundaries and accepted/provisional choices.

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

These are candidate platforms, not current support claims. Support requires the [validation gates](docs/architecture/validation-gates.md), [support matrix](docs/architecture/support-matrix.md) and [integration requirements](docs/integrations/README.md).

## Roadmap

The canonical [product roadmap](docs/roadmap.md) defines release scope, product invariants and acceptance gates. v0.1 is the desktop alpha; later v0.x stages cover an always-on hub, traffic analysis, wireless monitoring and optional capabilities. There is no release date or platform-support promise implied by that progression.

Installation and updates, hardware/service validation, resource budgets, privacy and recovery remain release requirements. [Issue #1](https://github.com/fijimunkii/cozysoc/issues/1) tracks execution against the documented roadmap.

The last completed full-controller Device Watch [storage workload](docs/development/device-watch-storage-workload.md) measured **474.50 MiB** on the earlier writer against the **30 MiB/day** target. The canonical batch writer is now the live Device Watch path, and its earlier adapter-only result was **28.67 MiB/day**. The unchanged full-controller workload must be rerun before claiming the target or release readiness. Prototype and codec results do not establish production resource budgets or 24-hour reliability.

## Security and privacy

Cozy SOC will process unusually sensitive household data, potentially including device identities, network metadata, DNS activity, and security findings. The project therefore treats least privilege, bounded retention, safe diagnostics, authenticated control paths, and explicit coverage limitations as product requirements rather than post-release hardening.

Please read [SECURITY.md](SECURITY.md) before reporting a vulnerability. Do not post sensitive household, credential, packet-capture, or vulnerability details in a public issue.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the [testing guide](docs/development/testing.md) for fixture isolation, cleanup and the limits of CI evidence. Changes should be tied to a scoped GitHub issue, use conventional commit subjects, and keep implementation claims aligned with tested behavior.

## License

Cozy SOC's first-party code and documentation are licensed under the [MIT License](LICENSE). The decision and alternatives are documented in [ADR 0001](docs/adr/0001-license.md).

Third-party engines, rules, drivers, data feeds, and bundled artifacts remain subject to their own licenses and distribution terms. Inclusion in the roadmap does not imply that Cozy SOC may redistribute a component.
