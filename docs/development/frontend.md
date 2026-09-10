# Shared frontend and local web surface

Issue #13 owns the first user-facing Cozy SOC experience. ADR 0003 is **Accepted** for a shared TypeScript/React UI. ADR 0004 remains **Proposed** for Tauri v2, so the shared UI remains browser-capable and does not depend on native renderer privileges.

The canonical roadmap now uses one user-facing **`cozysoc` executable with multiple explicit modes**. A single executable does not imply a single process lifetime: production service management owns `cozysoc serve`; closing `cozysoc web` or a future desktop window must not stop that controller process.

## Workspace

The shared UI lives in `ui/` and uses pinned React, TypeScript, Vite, and Vitest dependencies with a committed npm lockfile. Normal CI runs type checking, component/unit tests, a production build, and a real-process web/controller E2E in addition to the existing Go/controller gates.

The Vite development server still binds to `127.0.0.1`. It is a frontend developer convenience and is not a Cozy SOC management endpoint.

## `cozysoc web`

`cozysoc web` is a separate, unprivileged local UI process. In this v0.1 slice it:

- binds only to a **literal loopback IP**; wildcard, LAN, hostname, and public bind targets are rejected;
- chooses an ephemeral loopback port by default and prints the resulting authenticated local URL;
- serves an already-built `ui/dist` directory (override with `--ui-dir` for packaging/development layouts);
- exposes only allowlisted browser routes: the one-time `POST /api/session` bootstrap, authenticated session/CSRF metadata, typed reads for status/capabilities/coverage/devices/activity/networks, and narrowly typed network-enrollment / Device Watch control mutations;
- uses strict Host matching and, when an `Origin` header is present, requires the exact same local HTTP origin;
- requires the exact local origin on the browser-session bootstrap;
- rejects request bodies and query parameters on the parameterless coverage endpoint;
- rejects unknown `/api/*` paths instead of proxying arbitrary controller method names;
- applies bounded header/request-URI/body/time limits and local security headers; and
- talks to `cozysoc serve` through the existing `localapi.Client` over the authenticated Unix-domain socket.

### Separate browser session

Loopback and Host/origin checks are not treated as authentication. Each `cozysoc web` process generates three independent 256-bit random values:

1. a **one-time bootstrap value** printed only in the URL fragment (`#bootstrap=...`), which browsers do not send in HTTP requests; and
2. a separate **web-session value** that never appears in the URL or React state; and
3. a separate **CSRF value** that is returned only by authenticated same-origin session metadata and is not an authentication credential.

On first load, React reads the bootstrap value from the fragment, sends it once to the same-origin `POST /api/session`, and removes the fragment from browser history in a `finally` path. A valid, unused bootstrap is atomically consumed and replaced by an HttpOnly, Path `/`, SameSite=Strict session cookie. Reusing the bootstrap fails. The typed read endpoints require that cookie and otherwise return `401 web_session_required` without contacting the controller.

This browser session is deliberately separate from the controller credential. The controller session secret is loaded only inside the native Go process by the existing local API client. It is never returned by the web API, stored in React/browser storage, placed in a URL, reused as the web cookie, or made available to frontend code. The real-process E2E checks that the controller secret, one-time bootstrap, and web-session cookie are all absent from the coverage response.

The cookie is intentionally scoped to loopback HTTP for this local v0.1 surface, so it cannot use the `Secure` attribute without changing the transport. Remote/headless browser management remains separately gated by SEC-031 and must use authenticated encrypted transport; this local mechanism is not that future remote design.

`cozysoc web` can run while the controller is unavailable. After an authenticated browser session is established, a controller read failure returns a bounded `503 controller_unavailable` response and the UI presents an actionable unavailable state. The web process does **not** spawn, supervise, restart, or stop the controller.

State-changing browser endpoints now use an additional mutation guard rather than treating the SameSite cookie as sufficient CSRF protection. `GET /api/session` requires the HttpOnly web-session cookie and returns only the process-local CSRF value. A mutation is accepted only when all of the following hold:

- the HttpOnly web-session cookie authenticates;
- the request method is `POST`;
- `Origin` is present and exactly equals the current loopback origin;
- `X-Cozy-CSRF` matches the independent process-local CSRF value using constant-time comparison; and
- the endpoint-specific body/query/content-type contract validates before native controller access.

The CSRF value is designed for in-memory frontend use only. It is not stored in a URL, localStorage, controller configuration, or the UDS credential file. Cross-origin requests cannot obtain it through a readable response, and unknown `/api/*` routes remain unavailable.

The initial allowlisted mutation routes are `POST /api/networks/enroll`, `POST /api/device-watch/enable`, and `POST /api/device-watch/disable`; `GET /api/networks` supplies the read-only enrollment state needed by onboarding. They map only to the pre-existing typed controller methods. No arbitrary method name, command, file path, or network destination enters the native bridge. The UI does not call these mutations yet; the next #13 slice can build onboarding on this already-tested boundary.

Expected typed controller mutation errors are mapped to bounded HTTP outcomes (`400`, `404`, `409`, `412`, `501`); transport/internal failures remain a generic `503 controller_unavailable` without copying controller diagnostic strings into the browser.

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

The same envelope is available through `cozysoc coverage` and authenticated `GET /api/coverage`. Device-Watch-only operational/storage/queue detail is deliberately not copied into this browser-facing contract. When a second capability has a real coverage producer, the envelope can add another validated shared report without widening the browser API into arbitrary RPC.

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

The root UI establishes or reuses its local web session and then attempts the same-origin live coverage endpoint.

- A successful response is labeled **Live controller data**.
- Missing/invalid web session, failed bootstrap, or controller unavailability is labeled **Live monitoring unavailable** with actionable copy and retry plus an explicit demo choice.
- Synthetic data is **never** substituted automatically.
- Demo mode requires user action and remains labeled **Synthetic demo — This screen is not connected to live monitoring.**
- Returning from demo to live coverage is explicit and retries the authenticated local endpoint.

Demo data remains source code only; it is not written into controller storage or mixed with real observations/findings.

## Static asset boundary

This PR does not commit Vite build output and does not add a second bridge binary. `cozysoc web` serves a built UI directory so development and process E2E can exercise the real browser path now. #28 owns the release-packaging decision for whether final installers embed or co-install those static assets.

Static serving refuses directory listings and resolves symlinks before serving files so a requested path cannot escape the approved UI root. A regression fixture creates a symlink from the UI tree to an outside file and requires a 404 without serving the target.


## Product navigation and live devices

Current typed device read surfaces:

- **Devices** shows bounded Device Watch presence, scoped user labels, and a per-device evidence view that separates current presence from current/historical identity associations.
- **Activity** shows a bounded 24-hour Device Watch timeline: first observations, proven same-family address changes, and only the latest positive observation per otherwise-stable device. Silence never creates a departure event.
- `GET /api/devices/detail?device_id=...` — bounded current-scope identity evidence and retained source provenance for one device; raw observation payloads are not exposed.
- `GET /api/activity` — bounded low-noise Device Watch activity derived from retained normalized evidence; no raw payloads or inferred departures.

The validated information architecture now exposes **Overview**, **Devices**, **Activity**, **Coverage**, and **Tools**. Tools is backed by the real capability catalog rather than an empty navigation placeholder; broader Settings stays deferred until there are additional user-facing configuration choices.

Authenticated `GET /api/devices` is parameterless and read-only. It is backed by the existing controller `devices.list` method and returns only the bounded device-presence read model: configured scope, stable device ID, optional user label, first/last seen timestamps, `visible`/`uncertain` presence, and truncation. It does not expose raw observations, identity claims, database access, controller credentials, or a device mutation surface.

The Overview page summarizes only the current device-presence and shared-coverage evidence. Known limits remain explicit counts and next steps rather than a protection percentage. The Devices page does not invent `offline`; absence of recent positive evidence remains `uncertain` as defined by #11.

Synthetic demo data spans the same navigation but keeps the persistent synthetic-data banner on every section. Live and synthetic records are never mixed.

## Desktop shell boundary

This slice does not add Tauri or Wails. ADR 0004 remains Proposed until #5 proves packaging, service registration, UI-close/controller-survival, permission failure, sleep/reboot, resource, upgrade, and uninstall behavior. If Tauri is promoted, it should reuse the same `cozysoc` executable and web/shared-UI contract rather than create another controller or generic native bridge.

## Next steps

Useful #13 follow-ons are:

1. add browser-level accessibility, keyboard, scaling, and responsive tests around the first complete journey;
2. add validated external deep-link handling only when a capability actually declares a safe destination/context contract; and
3. let #28 choose the production static-asset packaging path without changing controller lifetime ownership.


## Live device labels

The Devices view can update or clear a user label only in live mode. Browser writes use the same process-local session, exact-Origin, and CSRF boundary as guided setup and call only `POST /api/devices/label`. The request contains a parsed device id plus the user label; an empty label clears the display label without deleting device evidence.

The controller remains authoritative for scope membership and label validation. It accepts only devices with retained evidence in the current authorized scope, applies the 160-byte trimmed/control-free label contract, and records real changes with the existing transactional audit event. A device that leaves the retained scope before save returns a bounded not-found result rather than being relabeled optimistically.

React validates the same 160 UTF-8 byte limit for immediate feedback, keeps the CSRF value only in the shared in-memory mutation client, renders all label text as ordinary text nodes, and reloads live device data after a successful change. Synthetic demo devices remain read-only.


### Device evidence detail

Live device rows can open a read-only evidence detail view through `GET /api/devices/detail?device_id=...`. The browser supplies only a device ID; the controller resolves the current Device Watch scope and refuses devices without retained evidence in that scope. The response is bounded to the most recent 100 identity-evidence rows and exposes claim/link/source metadata needed for explanation, not raw observation payload JSON.

Presence (`visible` / `uncertain`) and identity validity are intentionally separate. A retained MAC/IP association can be historical while the device remains listed, and a temporally current identity association is not itself proof of recent presence, trust, or safety. When a source observation has aged out before its longer-lived identity claim, the detail view says the raw source metadata expired instead of reconstructing it.


## Tools and capability presentation

The Tools page consumes two additional authenticated, parameterless read routes: `GET /api/status` and `GET /api/capabilities`. Both are browser-specific projections over existing typed UDS reads; they add no new controller method and no lifecycle mutation authority.

`GET /api/status` deliberately excludes PID, uptime, controller API internals, and credentials. It exposes only the controller build/version string, start time, configuration schema version, and the fact that management transport is the protected local Unix socket.

`GET /api/capabilities` deliberately does not serialize the full native manifest. The browser receives only the fields needed to explain a capability: display metadata, configured/ownership state, independent desired/process/verification state, declared target support level, privilege descriptions, resource-measurement status/budgets, provenance kind/license/version policy, verification contract, lifecycle action names, and a deep-link count. Native config schemas, dependency/input wiring, manifest evidence paths, provenance source URLs, and any configured values remain outside the browser contract.

The page keeps lifecycle and coverage semantics separate. `verified` means the capability's declared verification signals passed; it is not rendered as a whole-home protection claim. A built-in capability with `process=not-applicable` is shown as **Built into Cozy SOC**, not as a fake running process. `candidate`, `planned`, `limited`, and `tested` target states remain explicit, and an `unmeasured` resource profile is rendered as **Not measured yet** rather than receiving invented CPU/RAM/disk numbers.

Device Watch currently declares zero deep links. Tools therefore links back to Cozy SOC's own Devices, Activity, and Coverage views instead of presenting a broken upstream-engine link. Future external deep links remain separately gated on destination/context validation before the browser is allowed to navigate to them.
