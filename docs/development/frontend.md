# Shared frontend and local web surface

Issue #13 owns the first user-facing Cozy SOC experience. ADR 0003 is **Accepted** for a shared TypeScript/React UI. ADR 0004 remains **Proposed** for Tauri v2, so the shared UI remains browser-capable and does not depend on native renderer privileges.

The canonical roadmap now uses one user-facing **`cozysoc` executable with multiple explicit modes**. A single executable does not imply a single process lifetime: production service management owns `cozysoc serve`; closing `cozysoc web` or a future desktop window must not stop that controller process.

## Workspace

The shared UI lives in `ui/` and uses pinned React, TypeScript, Vite, and Vitest dependencies with a committed npm lockfile. Normal CI runs type checking, component/unit tests, a production build, and a real-process web/controller E2E in addition to the existing Go/controller gates.

The Vite development server still binds to `127.0.0.1`. It is a frontend developer convenience and is not a Cozy SOC management endpoint.

## `cozysoc web`

`cozysoc web` is a separate, unprivileged local UI process. In this v0.1 slice it:

- binds only to a **literal loopback IP**; wildcard, LAN, hostname, and public bind targets are rejected;
- chooses an ephemeral loopback port by default and prints the resulting local URL;
- serves an already-built `ui/dist` directory (override with `--ui-dir` for packaging/development layouts);
- exposes only the typed read-only `GET /api/coverage` endpoint;
- uses strict Host matching and, when an `Origin` header is present, requires the exact same local HTTP origin;
- rejects request bodies and query parameters on the parameterless coverage endpoint;
- rejects unknown `/api/*` paths instead of proxying arbitrary controller method names;
- applies bounded header/request-URI/time limits and local security headers; and
- talks to `cozysoc serve` through the existing `localapi.Client` over the authenticated Unix-domain socket.

The controller session secret is loaded only inside the native Go process by the existing local API client. It is never returned by `/api/coverage`, stored in React/browser storage, placed in a URL, or made available to frontend code.

`cozysoc web` can run while the controller is unavailable. In that case the API returns a bounded `503 controller_unavailable` response and the UI presents an actionable unavailable state. The web process does **not** spawn, supervise, restart, or stop the controller.

State-changing browser endpoints are intentionally not part of this slice. Before any are added, they must carry the authentication, Host/origin, CSRF, and authorization protections owned by #8 rather than treating loopback as sufficient authorization.

## Generic live coverage

The controller's existing detailed `device-watch.coverage` method remains the authoritative live read for the current capability. Native code projects that response into a generic envelope:

```json
{
  "as_of": "...",
  "reports": [
    {
      "capability_id": "device-watch",
      "configured": false,
      "state": "unconfigured",
      "reason": "...",
      "observation_points": [],
      "next_step": "..."
    }
  ]
}
```

The same envelope is available through `cozysoc coverage` and `GET /api/coverage`. Device-Watch-only operational/storage/queue detail is deliberately not copied into this browser-facing contract. When a second capability has a real coverage producer, the envelope can add another validated shared report without widening the browser API into arbitrary RPC.

The React loader accepts the envelope as `unknown`, validates the outer timestamp/report bounds, then passes each report through the existing strict shared coverage parser. Duplicate capability reports, malformed timestamps, unsupported shared fields, and oversized collections fail closed.

## Current product surface

`CoveragePanel` presents:

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

## Live, unavailable, and demo states

The root UI now attempts the same-origin live coverage endpoint first.

- A successful response is labeled **Live controller data**.
- A failed/unavailable response is labeled **Live monitoring unavailable** and offers retry plus an explicit demo choice.
- Synthetic data is **never** substituted automatically.
- Demo mode requires user action and remains labeled **Synthetic demo — This screen is not connected to live monitoring.**
- Returning from demo to live coverage is explicit and retries the local endpoint.

Demo data remains source code only; it is not written into controller storage or mixed with real observations/findings.

## Static asset boundary

This PR does not commit Vite build output and does not add a second bridge binary. `cozysoc web` serves a built UI directory so development and process E2E can exercise the real browser path now. #28 owns the release-packaging decision for whether final installers embed or co-install those static assets.

Static serving refuses directory listings and resolves symlinks before serving files so a requested path cannot escape the approved UI root.

## Desktop shell boundary

This slice does not add Tauri or Wails. ADR 0004 remains Proposed until #5 proves packaging, service registration, UI-close/controller-survival, permission failure, sleep/reboot, resource, upgrade, and uninstall behavior. If Tauri is promoted, it should reuse the same `cozysoc` executable and web/shared-UI contract rather than create another controller or generic native bridge.

## Next steps

Useful #13/#84 follow-ons are:

1. add `cozysoc dev` as an explicitly development-only orchestration command for controller + web;
2. add the first navigation/information architecture around Overview, Devices, Activity, Coverage, and Settings/Tools;
3. build network-enrollment/onboarding and Device Watch controls only after the required browser mutation protections are in place;
4. add browser-level accessibility/responsive tests around the first complete journey; and
5. let #28 choose the production static-asset packaging path without changing controller lifetime ownership.
