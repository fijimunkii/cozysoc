# Testing and release evidence

Cozy SOC uses several different test layers. A passing fixture is not treated as proof of a hardware- or deployment-specific claim.

## Normal pull-request CI

The default GitHub Actions workflow runs on the Linux reference runner and currently includes:

- repository hygiene and conventional-commit checks;
- Go formatting, `go vet`, and package/unit/integration tests;
- a black-box controller process smoke test;
- macOS arm64 cgo-free cross-compilation for the controller and Darwin-specific packages; and
- Foundation feasibility harness tests.

The process smoke test lives under `tests/e2e`. Ordinary `go test ./...` skips it unless `COZYSOC_E2E_BINARY` points to a built controller binary. CI builds the real `cozysoc-controller` executable and runs the E2E package separately so the black-box step is visible as its own gate.

## Controller process E2E boundary

The initial process E2E test exercises real executable/process boundaries rather than calling controller packages directly. It verifies:

- fresh state-directory creation and private file/socket permissions;
- controller process startup and readiness;
- authenticated `status`, `health`, `capabilities`, and `devices` CLI calls over the real Unix socket;
- single-instance rejection without rotating the active session secret;
- graceful interrupt shutdown and socket/session cleanup;
- clean restart with session-secret rotation;
- forced process termination; and
- stale socket/session recovery followed by another successful restart.

The test uses a temporary local state directory and does not inspect or scan any network.

## What this does not prove

A Linux process smoke test does not certify macOS service lifetime, macOS permissions, real ARP/NDP behavior, sleep/resume, switched-network visibility, mirror/TAP capture, or USB Wi-Fi behavior. Darwin cross-compilation proves only that the current macOS code path builds.

Those claims require the named real-hardware evidence tracked by the Foundation feasibility work and issue #29. Hardware-unavailable remains `untested`; CI fixture success must not promote it to tested support.

## Growth model

Add deterministic black-box scenarios as product surfaces become real. In particular, future E2E slices should cover explicit network enrollment, Device Watch configuration/labeling, truthful coverage verification, bounded failures such as low disk, and installation/service lifecycle once those product flows exist.

Keep the normal PR smoke suite deterministic, isolated, and reasonably fast. Longer sustained runs, packet labs, and hardware matrices belong in dedicated release/lab jobs rather than making routine PR CI depend on physical devices or household traffic.
