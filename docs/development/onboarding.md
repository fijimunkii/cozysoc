# v0.1 network onboarding and Device Watch setup

Issue #13 owns the first user-facing setup journey. This slice consumes the guarded browser mutation boundary established under #8 and #92; it does not add new controller authority or new browser endpoints.

## Consent model

Network authorization and Device Watch enablement are separate decisions.

1. Cozy SOC reads the bounded `GET /api/networks` candidate list.
2. No candidate is selected automatically.
3. The user selects one local interface and reviews its currently observed local prefixes.
4. `POST /api/networks/enroll` records that explicit scope authorization only.
5. Device Watch remains disabled until the user separately chooses **Enable Device Watch**.
6. Enablement still passes through the controller lifecycle preflight; a browser button is not permission to bypass platform/scope/current-interface checks.
7. Disabling Device Watch preserves the enrolled network authorization. There is no hidden unenroll operation in this flow.

The UI intentionally does not describe enrollment as monitoring and does not describe enabled intent as proof that current evidence is healthy. Coverage remains the authority for verified observation state and gaps.

## Browser mutation client

The React app creates one setup client for the lifetime of the mounted `App` component. The client fetches the authenticated CSRF value from `GET /api/session` only when the first mutation is attempted, validates it, and keeps it only in that client closure. It is never written to localStorage/sessionStorage, a URL, app evidence data, or controller configuration.

Each mutation uses the existing same-origin HttpOnly session cookie plus `X-Cozy-CSRF`. The browser supplies the request Origin; frontend code does not attempt to synthesize or override it.

The frontend accepts only the existing allowlisted routes:

- `POST /api/networks/enroll`
- `POST /api/device-watch/enable`
- `POST /api/device-watch/disable`

It validates bounded network and Device Watch response shapes before using them.

## Setup states

The Overview setup card represents these states explicitly:

- **Network setup unavailable** — setup-specific enumeration failed, while already-loaded device/coverage evidence remains usable.
- **Choose network** — eligible interfaces are shown with no default selection.
- **Review authorization** — the selected interface/prefix scope is shown again before mutation.
- **Network authorized / Device Watch off** — authorization succeeded but monitoring has not started.
- **Device Watch enabled** — desired monitoring state is enabled; friendly coverage state is shown separately.
- **Setup paused** — session-only “Not now” state with an explicit Resume action.
- **Disable confirmation** — pausing monitoring requires a second confirmation and explicitly says network authorization remains.

Failed mutations never optimistically patch live state. The app reloads coverage, devices, and network authorization from the controller after a successful mutation. If a mutation fails, copy states that no additional change is assumed and preserves the distinction between authorization and monitoring.

## Demo boundary

Synthetic demo mode never renders live setup controls. Demo data remains labeled synthetic and cannot trigger network enrollment or Device Watch mutations.

## Test boundary

Component tests cover explicit network choice, review-before-enrollment, separate enablement, prerequisite failure, confirmation-before-disable, pause/resume, and setup-only network-read failure. The web mutation security/process tests from #92 remain the authority for origin/CSRF/session enforcement and the no-CI-network-enrollment guarantee.
