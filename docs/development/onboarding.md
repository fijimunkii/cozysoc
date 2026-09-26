# v0.1 network onboarding and Device Watch setup

Issue #13 owns the first user-facing setup journey. The guarded browser mutation boundary established under #8 and #92 carries the typed setup operations.

## Consent model

Network authorization and Device Watch enablement are separate decisions.

1. Cozy SOC reads the bounded `GET /api/networks` candidate list.
2. No candidate is selected automatically.
3. The user selects one local interface and reviews its currently observed local prefixes.
   The review freezes the interface, index and prefixes shown. If a later network
   read changes or removes that candidate, authorization is disabled until the
   user goes back and reviews the current details again.
4. `POST /api/networks/enroll` sends that reviewed binding. The browser route
   requires it; the controller recaptures the current binding and rejects an
   index or prefix mismatch before writing authorization. The native
   `network-enroll INTERFACE` command retains its separate interface-name-only
   contract. Enrollment records explicit scope authorization only.
5. Device Watch remains disabled until the user separately chooses **Enable Device Watch**.
6. Enablement still passes through the controller lifecycle preflight; a browser button is not permission to bypass platform/scope/current-interface checks.
7. The user verifies Device Watch coverage after enablement. Only a current `active-limited` report completes this setup step; it establishes limited passive neighbor evidence, not whole-network or traffic visibility.
8. Disabling Device Watch preserves the enrolled network authorization. To change networks, the user must stop Device Watch, review the current authorization, and explicitly retire it. Retirement does not delete historical evidence or authorize a replacement. A new network requires fresh enrollment and separate enablement.

The UI explains that network authorization and resulting device evidence stay on this machine by default; setup needs no account or router change. It intentionally does not describe enrollment as monitoring or enabled intent as proof that current evidence is healthy. Coverage remains the authority for verified observation state and gaps.

## Browser mutation client

The React app creates one setup client for the lifetime of the mounted `App` component. The client fetches the authenticated CSRF value from `GET /api/session` only when the first mutation is attempted, validates it, and keeps it only in that client closure. It is never written to localStorage/sessionStorage, a URL, app evidence data, or controller configuration.

Each mutation uses the existing same-origin HttpOnly session cookie plus `X-Cozy-CSRF`. The browser supplies the request Origin; frontend code does not attempt to synthesize or override it.

The frontend accepts only these allowlisted routes:

- `POST /api/networks/enroll`
- `POST /api/networks/retire`
- `POST /api/device-watch/enable`
- `POST /api/device-watch/disable`

It validates bounded network and Device Watch response shapes before using them.
The browser enrollment request carries only the validated reviewed interface,
index and prefixes. Retirement carries the reviewed scope ID and checks that
it still matches the active scope. A controller precondition or conflict
requires a fresh read and review rather than an optimistic UI update.

## Setup states

The Overview setup card represents these states explicitly:

- **Network setup unavailable** — setup-specific enumeration failed, while already-loaded device/coverage evidence remains usable.
- **Choose network** — eligible interfaces are shown with no default selection.
- **Review authorization** — the selected interface/prefix scope is shown again before mutation.
- **Network authorized / Device Watch off** — authorization succeeded but monitoring has not started.
- **Awaiting coverage evidence** — Device Watch is enabled but a current coverage report is absent or unverified; setup remains at step three with a read-only refresh and a link to Coverage.
- **Current limited coverage** — Device Watch reports `active-limited`; setup shows completion while explicitly describing passive neighbor-cache limits and linking to Coverage for evidence time and gaps.
- **Coverage needs review** — degraded, stale, disconnected, unavailable, or permission-required coverage never appears complete; the report's next step and Coverage view guide recovery.
- **Setup paused** — session-only “Not now” state with an explicit Resume action.
- **Disable confirmation** — pausing monitoring requires a second confirmation and explicitly says network authorization remains.
- **Retirement confirmation** — after monitoring stops, changing networks requires a second review of the enrolled interface and an explicit withdrawal. Historical evidence stays local; setup returns to network choice after a successful controller read.

Failed mutations never optimistically patch live state. The app reloads coverage, devices, and network authorization from the controller after a successful mutation. If a mutation fails, copy states that no additional change is assumed and preserves the distinction between authorization and monitoring.

Opening enrollment, retirement, or disable confirmation moves keyboard focus to its heading; Back restores focus to the action that opened it. “Not now” and Resume move focus to the paused or resumed heading. After a successful enrollment, retirement, enable, or disable action changes the setup step on the next controller read, focus moves to the new step heading. Initial render and routine evidence refreshes do not move focus.

## Demo boundary

Synthetic demo mode never renders live setup controls. Demo data remains labeled synthetic and cannot trigger network enrollment or Device Watch mutations.

## Test boundary

Component and controller tests cover explicit network choice, a frozen reviewed binding, rejection when the interface changes before storage, separate enablement, focus through confirmation, pause/resume, and successful step changes without refresh focus theft, prerequisite failure, coverage verification and review navigation, confirmation-before-disable, and setup-only network-read failure. The web mutation security/process tests from #92 remain the authority for origin/CSRF/session enforcement and the no-CI-network-enrollment guarantee.
