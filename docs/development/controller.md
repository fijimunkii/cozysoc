# v0.1 controller bootstrap

Issue #7 introduces the first production-oriented Cozy SOC process: an independent Go controller with a small typed local API.

## Current slice

The current controller provides:

- a versioned configuration file in a private state directory;
- an independent long-running process with graceful signal shutdown;
- a read-only versioned API with `status` and `health` methods;
- a permissioned Unix-domain socket rather than a TCP/localhost listener;
- single-instance protection through the local socket;
- bounded request size, deadline, and concurrent-client count;
- JSON structured logs that do not log request payloads;
- a health ticker that records long scheduling/sleep gaps; and
- a CLI client using the same typed API.

This is intentionally not the entire #7 scope. macOS service registration, reboot/crash lifecycle, final Tauri packaging, and real sleep/resume evidence remain gated by #5/#56. Issue #7 should remain open until those platform-specific acceptance criteria pass.

## Run locally

```bash
go run ./cmd/cozysoc-controller serve --state-dir /tmp/cozysoc-dev
```

From another terminal:

```bash
go run ./cmd/cozysoc-controller status --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc-controller health --state-dir /tmp/cozysoc-dev
```

The controller creates `/tmp/cozysoc-dev` mode `0700`, `config.json` mode `0600`, and `controller.sock` mode `0600` on Unix-like systems.

## Security boundary

This slice deliberately exposes only read operations. It does **not** claim to complete issue #8.

Socket filesystem permissions provide a narrow bootstrap boundary, but the release design still requires the explicit local-caller/peer-authentication work, desktop-shell bridge policy, secret storage, and privileged-helper contract in #8. Do not add mutating methods to this transport until that work defines their authorization model.

The controller does not expose HTTP/TCP, a generic command runner, direct database access, service-manager commands, capture commands, router credentials, or Docker control.

## API framing

Each local connection sends one newline-terminated JSON request and receives one JSON response. The API version is independent from the controller binary version.

Example request:

```json
{"version":1,"id":"example","method":"status"}
```

Unknown methods and API-version mismatches return typed errors. Requests are capped at 64 KiB and connections have a five-second deadline.

This framing is intentionally small and replaceable; the domain API contract matters more than the transport serialization choice.
