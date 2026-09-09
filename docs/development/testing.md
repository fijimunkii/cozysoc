# Testing and release evidence

Cozy SOC uses several different test layers. A passing fixture is not treated as proof of a hardware- or deployment-specific claim.

## Normal pull-request CI

The default GitHub Actions workflow runs on the Linux reference runner and currently includes:

- repository hygiene and conventional-commit checks;
- Go formatting, `go vet`, and package/unit/integration tests;
- a black-box controller process E2E suite;
- macOS arm64 cgo-free cross-compilation for the controller and Darwin-specific packages; and
- Foundation feasibility harness tests.

The process E2E tests live under `tests/e2e`. Ordinary `go test ./...` skips them unless `COZYSOC_E2E_BINARY` points to a built controller binary. CI builds the real `cozysoc-controller` executable and runs the E2E package separately so the black-box step is visible as its own gate.

## Controller process E2E boundary

The process E2E suite exercises real executable/process boundaries rather than calling controller packages for the operation under test. It currently verifies:

- fresh state-directory creation and private file/socket permissions;
- controller process startup and readiness;
- authenticated `status`, `health`, `capabilities`, and `devices` CLI calls over the real Unix socket;
- single-instance rejection without rotating the active session secret;
- graceful interrupt shutdown and socket/session cleanup;
- clean restart with session-secret rotation;
- forced process termination;
- stale socket/session recovery followed by another successful restart; and
- the authenticated `device-label` mutation, including persistence across restart, idempotent repeat behavior, explicit clear, rejected invalid/unknown targets, and durable audit transitions.

The device-label E2E uses a deterministic SQLite fixture only to seed one scope/device while the controller is stopped. The state-changing requests themselves travel through the built CLI, session secret, Unix socket, controller authorization, scoped storage transaction, and durable audit path. Direct database inspection occurs only after shutdown to verify audit evidence.

The suite uses temporary local state directories and does not inspect or scan any network.

## What this does not prove

A Linux process E2E suite does not certify macOS service lifetime, macOS permissions, real ARP/NDP behavior, sleep/resume, switched-network visibility, mirror/TAP capture, or USB Wi-Fi behavior. Darwin cross-compilation proves only that the current macOS code path builds.

Those claims require the named real-hardware evidence tracked by the Foundation feasibility work and issue #29. Hardware-unavailable remains `untested`; CI fixture success must not promote it to tested support.

## Growth model

Add deterministic black-box scenarios as product surfaces become real. Next useful expansions include explicit network enrollment, Device Watch configuration through the public mutation surface, truthful coverage verification, bounded failures such as low disk, and installation/service lifecycle once those product flows exist.

Keep the normal PR E2E suite deterministic, isolated, and reasonably fast. Longer sustained runs, packet labs, and hardware matrices belong in dedicated release/lab jobs rather than making routine PR CI depend on physical devices or household traffic.
