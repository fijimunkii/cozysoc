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
- authenticated read commands over the real Unix socket;
- the `device-watch-coverage` CLI/read path and its safe unconfigured default state;
- single-instance rejection without rotating the active session secret;
- graceful interrupt shutdown and socket/session cleanup;
- clean restart with session-secret rotation;
- forced process termination and stale socket/session recovery;
- the authenticated `device-label` mutation, including restart persistence, idempotent repeat behavior, explicit clear, rejected invalid/unknown targets, and durable audit transitions;
- explicit network enrollment, including local candidate discovery, real authenticated enrollment, idempotent re-enrollment, unsafe-interface rejection, conflicting-network refusal, restart persistence, and durable audit evidence; and
- Device Watch control on the Linux reference runner, including safe rejection of unsupported enablement without persisting enabled intent and safe idempotent disable.

The device-label E2E uses a deterministic SQLite fixture only to seed one scope/device while the controller is stopped. The state-changing requests themselves travel through the built CLI, session secret, Unix socket, controller authorization, scoped storage transaction, and durable audit path. Direct database inspection occurs only after shutdown to verify audit evidence.

The network-enrollment E2E does not need a packet/network fixture. It reads the Linux CI runner's local interface/address metadata, selects an eligible interface, and exercises `network.enroll` through the real controller process. Candidate listing and enrollment capture do **not** ping, resolve, scan, or otherwise send discovery traffic. Direct SQLite inspection again occurs only after shutdown to verify the audit event.

The Device Watch control E2E builds on that real enrollment. Linux intentionally does not implement the v0.1 passive runtime, so `device-watch-enable` must fail with `precondition_failed` before enabled intent or a capability-intent audit transition is written. CI then proves `device-watch-disable` is a safe no-op and restart keeps Device Watch disabled. Successful live start/stop semantics are covered by deterministic lifecycle-driver/controller tests and Darwin cross-compilation; they are not misrepresented as Linux runtime evidence.

The coverage-read process E2E deliberately checks only the fresh-controller `unconfigured` state on Linux. It proves the real CLI/authenticated Unix-socket/controller projection is wired without pretending Linux produced macOS ARP/NDP evidence.

Device Watch coverage verification is tested deterministically at the storage/evaluator/lifecycle boundary. Fixtures prove that evidence time wins over insertion order, logical expiry is respected, current evidence with both ARP/NDP sources available can verify the limited capability, a current gap in either source degrades it, both sources unavailable degrades it, old evidence becomes stale, unsupported/future/contradictory evidence fails closed, and a quiet sample with zero neighbors can still be a healthy current heartbeat. These tests prove the state semantics; they do not certify real macOS ARP/NDP behavior.

## What this does not prove

A Linux process E2E suite does not certify macOS service lifetime, macOS permissions, real ARP/NDP observation behavior, sleep/resume, switched-network visibility, mirror/TAP capture, or USB Wi-Fi behavior. Darwin cross-compilation proves only that the current macOS code path builds.

Network-enrollment/control/coverage-read E2E proves authorization and control/read-plane behavior on the Linux runner; it does not prove that Linux provides useful Device Watch presence evidence. The v0.1 passive observation source remains macOS-first until real platform evidence says otherwise.

Those claims require the named real-hardware evidence tracked by the Foundation feasibility work and issue #29. Hardware-unavailable remains `untested`; CI fixture success must not promote it to tested support.

## Growth model

Add deterministic black-box scenarios as product surfaces become real. Next useful expansions include richer #12 sensor/disconnection/ingestion health, bounded failures such as low disk, identity correction flows, and installation/service lifecycle once those product flows exist.

Keep the normal PR E2E suite deterministic, isolated, and reasonably fast. Longer sustained runs, packet labs, and hardware matrices belong in dedicated release/lab jobs rather than making routine PR CI depend on physical devices or household traffic.
