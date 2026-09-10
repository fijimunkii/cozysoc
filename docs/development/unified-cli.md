# Unified `cozysoc` command surface

Issue #84 tracks consolidation of Cozy SOC's user-facing command surface under one executable while preserving independent production process lifetimes.

Current modes:

- `cozysoc serve` — independently managed controller process;
- `cozysoc status`, `health`, `capabilities`, `devices`, `networks`, `coverage`, and capability-specific typed commands — local authenticated CLI access;
- `cozysoc web` — separate authenticated loopback UI process backed by the controller's protected UDS client; and
- `cozysoc dev` — **development-only** orchestration for the controller + web experience.

`cozysoc dev` first validates the same loopback/UI inputs required by `cozysoc web`. It then checks the configured state directory for a healthy authenticated controller. If one already exists, dev reuses it and never starts, stops, or rotates that controller. If none is available, dev starts the **same `cozysoc` executable with the fixed `serve --state-dir ...` subcommand**, waits for the authenticated UDS status to report the child PID, and treats only that child as temporary dev-owned state.

When dev exits, it stops only a temporary controller that it started itself. A pre-existing service continues with the same PID and controller session secret. Web startup failure also cleans up a dev-owned controller rather than leaving an accidental background service behind.

The self-spawn is a fixed internal orchestration path, not a generic process-execution API: users cannot supply another executable, subcommand, or arbitrary argv through `dev`. Nothing about this development helper grants process authority to React or the browser.

Production lifecycle evidence must continue to exercise the independently service-managed `cozysoc serve` path. A successful `cozysoc dev` run is intentionally **not** proof that monitoring survives UI exit, login-session exit, or reboot.

The single executable is a packaging and operator-experience choice, not a collapse of trust or lifecycle boundaries.
