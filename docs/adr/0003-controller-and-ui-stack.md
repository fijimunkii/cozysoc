# ADR 0003: Use Go for the controller and TypeScript/React for the shared UI

- **Status:** Accepted
- **Date:** 2026-09-08
- **Decision owners:** Cozy SOC maintainers
- **Issue:** #3

## Context

The controller needs strong concurrency/networking support, straightforward static binaries for multiple operating systems/architectures, a small background footprint, and good libraries for local services and integrations.

The UI should be reusable between the desktop shell and a future explicitly enabled browser interface on a headless hub. It needs an accessible component ecosystem and a typed contract with the controller.

The UI framework must not dictate controller lifetime or become the authorization boundary.

## Decision

Use:

- **Go** for the controller/core service and most portable integration/domain code; and
- **TypeScript + React** for the shared user interface.

Keep platform-specific privileged/lifecycle code behind narrow adapters. A desktop-shell implementation may introduce a small amount of another systems language when the selected shell requires it; that code is intentionally thin and does not become a second controller.

The domain API is versioned independently of the UI. Frontend-generated types improve correctness but never replace controller-side validation/authorization.

## Alternatives considered

### All-TypeScript/Node controller

Technically viable, but adds a runtime/distribution footprint we do not need for the long-running core and is less aligned with the desired single-binary headless sensor/hub path.

### Rust controller

Strong technical fit, especially for privileged/system components, but it would increase implementation complexity for broad network/service integration work without a demonstrated advantage over Go for the controller. Rust may still be used by a thin desktop shell or narrowly scoped native code.

### Native UI per platform

Would maximize OS integration but fragment the frontend and duplicate the product experience before cross-platform requirements are proven.

## Consequences

- Controller/domain packages must avoid depending on desktop-window APIs.
- React talks through a versioned controller contract, not the database or engines directly.
- Headless hub mode can use the same Go core without bundling a desktop WebView.
- Desktop packaging must build a compatible Go controller binary per supported target.
- The selected desktop shell is a separate Proposed decision so #5 can still change it without changing the controller/UI stack.

## Validation

#5 measures the actual controller/background and desktop-shell resource/startup targets. If Go cannot meet the core budgets under the reference workload, record measurements and revisit the budget or architecture explicitly rather than silently expanding it.
