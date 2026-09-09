# Shared frontend foundation

Issue #13 owns the first user-facing Cozy SOC experience. ADR 0003 is **Accepted** for a shared TypeScript/React UI. ADR 0004 remains **Proposed** for Tauri v2, so the first frontend slice is deliberately browser-capable React code without a Rust/Tauri shell or native renderer privileges.

## Workspace

The shared UI lives in `ui/` and uses pinned React, TypeScript, Vite, and Vitest dependencies with a committed npm lockfile. Normal CI runs type checking, component/unit tests, and a production build in addition to the Go/controller gates.

The development server binds to `127.0.0.1` rather than all interfaces. This is a developer convenience only; it is not the future authenticated browser-management listener described by the architecture.

## Current product surface

The first reusable product component is `CoveragePanel`. It consumes the capability-independent nested coverage contract added by #12 rather than Device-Watch-specific operational fields.

It presents:

- aggregate capability state using calm, bounded copy;
- one or more explicit observation points;
- configured scope;
- currently verified scope;
- expected-but-unverified scope as a separate concept;
- evidence time/freshness and observation cadence;
- directions only when evidence says they are observed;
- first-class known limits with one next step each; and
- technical source state behind a secondary disclosure.

There is no percentage, protection score, or green "safe" badge. `active-limited` is rendered as **Active, with limits**.

## Trust boundary

The UI does not read SQLite, launch processes, manage services, access packet capture, or receive generic filesystem/network authority. The coverage parser accepts `unknown` input and reconstructs a bounded allowlisted object before presentation. Unknown fields are discarded; invalid state/dimension/direction/cadence values fail closed.

React renders all report text as ordinary text nodes. This slice introduces no `dangerouslySetInnerHTML` path. A regression test passes markup-like hostile text through the component and verifies that no DOM element is created from it.

Frontend types improve correctness but do not replace controller-side validation or authorization.

## Synthetic demo boundary

The workspace currently has **no renderer-to-controller transport**. The root app therefore uses a deterministic synthetic coverage fixture so component work can proceed without pretending the browser has live controller authority.

A persistent banner says **Synthetic demo — This screen is not connected to live monitoring.** Tests require that labeling. Demo data is source code only; it is not written into controller storage or mixed with real observations/findings.

## Desktop shell boundary

This slice does not add Tauri or Wails. ADR 0004 remains Proposed until #5 proves packaging, service registration, UI-close/controller-survival, permission failure, sleep/reboot, resource, upgrade, and uninstall behavior. When a shell is added, it must remain a thin client to the independently managed controller and expose only narrow native commands.

## Next steps

Useful #13 follow-ons are:

1. define the narrow authenticated renderer/controller read bridge without exposing the session secret through browser storage;
2. add the first navigation/information architecture around Overview, Devices, Activity, Coverage, and Settings/Tools;
3. replace the demo fixture with live coverage only after that bridge is proven, while retaining an explicitly selectable demo mode;
4. build network-enrollment/onboarding and Device Watch controls on the existing capability-specific mutation APIs; and
5. add browser-level accessibility/responsive tests once the first complete journey exists.
