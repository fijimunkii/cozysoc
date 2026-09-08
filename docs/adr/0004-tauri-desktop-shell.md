# ADR 0004: Provisionally use Tauri v2 as the thin desktop shell

- **Status:** Proposed
- **Date:** 2026-09-08
- **Decision owners:** Cozy SOC maintainers
- **Issues:** #3, #5, #8, #28

## Context

Cozy SOC needs a small native desktop shell around the shared React UI. The shell must remain a client to an independently managed Go controller rather than absorbing controller responsibility.

Security properties matter more than convenience bindings: a compromised renderer must not automatically gain arbitrary process/system authority.

## Current upstream evidence

Research performed 2026-09-08:

- Tauri v2 documents an explicit trust boundary between WebView code and native core code, with command/plugin capabilities controlling exposed system resources: <https://v2.tauri.app/security/>.
- Tauri documents CSP hardening and recommends avoiding remote content in privileged application WebViews: <https://v2.tauri.app/security/csp/>.
- Tauri v2 supports bundling target-specific external binaries (`externalBin`), which is useful for distributing the Go controller artifact: <https://v2.tauri.app/develop/sidecar/>.
- Tauri 2.11.1 (2026-05-06) included security fixes around remote-origin IPC ACL handling, reinforcing the need to pin and update the shell rather than relying on framework name alone: <https://v2.tauri.app/release/tauri/v2.11.1/>.
- Wails v2 is the current stable Wails release and integrates Go with native WebViews/React directly: <https://v2.wails.io/docs/introduction/>.
- Wails v3 entered beta on 2026-08-02. Its server builds and explicit application/service model are interesting, but upstream still describes v3 as beta rather than the current stable release: <https://v3.wails.io/blog/>.

## Proposed decision

Use **Tauri v2 + React/TypeScript** for the first desktop shell, while keeping the controller as the separately built Go service selected in ADR 0003.

The shell should:

- bundle/distribute the correct controller artifact for the target architecture;
- expose only narrow UI/native commands;
- keep a restrictive CSP and no remote application content by default;
- connect to the controller through the protected local IPC boundary;
- participate in explicit service registration/update/uninstall flows without giving renderer code generic service-manager authority; and
- remain disposable from the controller's perspective: renderer reload or shell exit does not affect controller lifetime.

**Tauri sidecar process spawning is not the production controller lifecycle.** `externalBin` is useful as a packaging mechanism, but the OS service manager must own the installed controller after registration.

## Alternatives considered

### Wails v2

Advantages:

- stable;
- excellent Go/React developer experience;
- native OS WebView rather than a bundled browser;
- no Rust toolchain for the ordinary desktop shell.

Reason not currently preferred: its primary convenience is direct Go↔JavaScript binding inside the GUI application. Cozy SOC deliberately wants the real Go controller outside the GUI lifecycle, so much of that benefit is reduced. Tauri's capability-oriented renderer/native boundary is a better fit for a deliberately thin shell.

This is not a claim that Wails is insecure. A #5 experiment may still show Wails produces materially simpler, safer packaging for our separated controller model.

### Wails v3 beta

Attractive future properties include server builds and a redesigned service/application model, but adopting a beta framework would add avoidable release churn to the first security-sensitive desktop path. Re-evaluate after general availability or if #5 exposes a blocking Tauri issue.

### Electron

Viable and mature, but Cozy SOC does not currently need a bundled Chromium/Node runtime or its larger general-purpose application surface for a thin local shell. Keep as fallback rather than initial direction.

## Security constraints

Regardless of shell:

- no generic `shell.exec`/spawn permission exposed to renderer code;
- no unrestricted filesystem, service-manager, capture, router, or container permission;
- no remote HTML/JavaScript loaded into the privileged application WebView;
- external URLs open outside the privileged app context through an allowlisted/safe opener path;
- controller authorization is not delegated to frontend code; and
- framework/plugin versions are pinned and included in dependency/security update policy.

## Promotion gate

Promote this ADR to **Accepted** only after #5 demonstrates on the desktop reference candidate:

1. build/package/signing feasibility for shell + target Go controller artifact;
2. explicit service registration of the controller;
3. UI close/reopen without controller interruption or duplication;
4. denied-permission and failed-registration behavior;
5. sleep/resume and reboot behavior;
6. controller and shell resource/startup measurements against the architecture targets; and
7. clean upgrade/uninstall ownership boundaries.

If meeting those requirements requires broad renderer privilege, routine controller root/admin execution, or fragile GUI-owned process supervision, revisit Wails/native packaging before acceptance.
