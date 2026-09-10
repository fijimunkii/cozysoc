# Unified `cozysoc` command surface

Issue #84 tracks consolidation of Cozy SOC's user-facing command surface under one executable while preserving independent production process lifetimes.

Current modes in this slice:

- `cozysoc serve` — independently managed controller process;
- `cozysoc status`, `health`, `capabilities`, `devices`, `networks`, `coverage`, and capability-specific typed commands — local authenticated CLI access;
- `cozysoc web` — separate authenticated loopback UI process backed by the controller's protected UDS client.

`cozysoc dev` is intentionally deferred to the next slice. It may orchestrate controller + web for development only and must never become evidence that production controller lifetime is independent from the UI.

The single executable is a packaging and operator-experience choice, not a collapse of trust or lifecycle boundaries.
