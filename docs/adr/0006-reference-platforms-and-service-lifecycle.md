# ADR 0006: Provisionally target macOS desktop and Ubuntu LTS hub/sensor first

- **Status:** Proposed
- **Date:** 2026-09-08
- **Decision owners:** Cozy SOC maintainers
- **Issues:** #3, #5, #15, #28

## Context

A cross-platform desktop framework can compile for an operating system without proving that Cozy SOC's background controller, privileged helpers, capture paths, signing, sleep/restart behavior, and integrated engines work there.

The project needs a narrow first validation matrix rather than a promise of feature parity across every desktop and home-server platform.

## Current platform evidence

Research performed 2026-09-08:

- Apple documents `SMAppService` on macOS 13+ for registering bundled login items, launch agents, and launch daemons, providing the modern service-registration path for an app-bundled helper/controller: <https://developer.apple.com/documentation/servicemanagement/smappservice>.
- Microsoft documents the Service Control Manager and Windows Services for long-running background services that can operate independently of an interactive user session: <https://learn.microsoft.com/en-us/windows/win32/services/about-services>.
- Canonical lists Ubuntu 26.04 LTS as released April 2026 with standard support through 2031, on architectures including amd64 and arm64: <https://ubuntu.com/about/release-cycle>.

## Proposed decision

### First desktop reference candidate

**macOS 13+ on Apple Silicon**.

Reasons:

- it provides the modern `SMAppService` lifecycle model required by the architecture;
- it is a strong environment for validating the desktop shell, independent Go controller, local IPC, sleep/resume, permissions, signing/notarization, and service registration as one coherent path; and
- limiting the first matrix lets #5 generate real evidence before expanding support.

This is a **candidate**, not a support claim. Exact minimum macOS version and architecture matrix can change from #5 evidence.

### First headless hub / advanced sensor reference candidate

**Ubuntu Server 26.04 LTS**, evaluating both **amd64 and arm64** on real hardware.

Reasons:

- current LTS support horizon;
- suitable headless service model for an always-on home host;
- practical target for packet/wireless engines that generally have their strongest support on Linux; and
- arm64 is important for compact home hardware, but must be proven separately rather than inferred from Go/Linux portability.

### Planned later platforms

- **Windows 10/11 desktop** — planned; the controller should use Windows Services/SCM when implemented.
- **macOS Intel** — planned/unvalidated after the first alpha matrix.
- **Linux desktop** — planned separately from headless hub/sensor support.
- **NAS/router appliances and arbitrary Linux distributions** — unsupported/unvalidated until a dedicated compatibility path exists.

## Packaging/lifecycle model

The desktop application package may contain the target Go controller binary as a bundled artifact (for example through the selected shell's external-binary packaging support), but runtime lifecycle belongs to the OS service mechanism.

### macOS

Proposed flow:

1. signed app bundle contains the controller/helper artifacts;
2. explicit setup invokes the supported ServiceManagement registration path;
3. launchd owns controller start/restart semantics;
4. UI connects locally when open; and
5. uninstall/update coordinates unregister/replace/re-register without renderer-level generic service authority.

### Windows

Future equivalent uses the Service Control Manager rather than a tray/UI child process.

### Linux hub

Use the distribution's service manager (systemd on the Ubuntu reference path) for the controller/sensor lifecycle. Packaging format and installer UX remain #28 decisions.

## Alternatives considered

### Claim all Tauri/Wails build targets immediately

Rejected. UI build support says little about independent service lifetime, capture/USB access, privileged helpers, signing, integrations, or support burden.

### Linux-only product first

Would simplify advanced network sensing but conflicts with the product's desktop-first onboarding goal.

### Always require a dedicated Linux box

Rejected. Desktop mode must deliver device visibility and honest coverage by itself; the hub is an upgrade path for continuity/deeper observation.

### Start Windows and macOS simultaneously

Deferred to reduce validation surface. The architecture preserves Windows as a planned platform but does not require two installer/service implementations before the first vertical slice proves itself.

## Promotion gate

Promote this ADR to Accepted only after #5 records real evidence for:

- macOS service registration, UI-independent lifetime, crash/restart, reboot, denied permission, sleep/resume, local IPC, signing/package feasibility, and resource targets;
- Ubuntu 26.04 LTS service/capture behavior on named amd64 and/or arm64 hardware;
- exact tested architecture/OS versions in the support matrix; and
- cleanup/uninstall behavior for every platform claimed.

#6 must additionally confirm that the selected initial engines actually support the proposed Linux architectures/versions with acceptable licenses and privileges.
