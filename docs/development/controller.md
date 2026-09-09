# v0.1 controller bootstrap

Issue #7 introduces the first production-oriented Cozy SOC process: an independent Go controller with a small typed local API.

## Current slice

The current controller provides:

- a versioned configuration file in a private state directory;
- an independent long-running process with graceful signal shutdown;
- a versioned local API with read methods such as `status`, `health`, `capabilities.list`, and `devices.list`;
- an explicitly allowlisted, controller-authorized `device.label` mutation rather than a generic write surface;
- a permissioned Unix-domain socket rather than a TCP/localhost listener;
- single-instance protection through the local socket;
- bounded request size, deadline, and concurrent-client count;
- strict top-level request decoding and typed per-method parameters;
- JSON structured logs that do not log request payloads;
- a health ticker that records long scheduling/sleep gaps; and
- a CLI client using the same typed API.

macOS service registration, reboot/crash lifecycle, final Tauri packaging, and real sleep/resume evidence remain gated by #5/#56.

## Run locally

```bash
go run ./cmd/cozysoc-controller serve --state-dir /tmp/cozysoc-dev
```

From another terminal:

```bash
go run ./cmd/cozysoc-controller status --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc-controller health --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc-controller capabilities --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc-controller devices --state-dir /tmp/cozysoc-dev
```

Once Device Watch is configured and a device exists in its enrolled scope, its user label can be changed through the narrow mutation surface:

```bash
go run ./cmd/cozysoc-controller device-label --state-dir /tmp/cozysoc-dev DEVICE_ID "Living Room TV"
```

An empty label argument clears the user label.

The controller creates the state directory mode `0700`, `config.json` and the SQLite database mode `0600`, and `controller.sock` / `controller.auth` mode `0600` while the service is running.

## Security boundary

The local API is not a generic controller RPC or database API. Write operations must be individually defined, authenticated, authorized, bounded, and audited.

The current `device.label` mutation:

- is authenticated through the same per-controller session secret and OS peer-identity checks as reads;
- accepts strict typed parameters only;
- is pinned to the already configured Device Watch `NetworkScope`;
- independently revalidates the target's retained scope evidence in storage;
- validates the user label before persistence; and
- commits the device change and durable audit event in one SQLite transaction.

The controller does not expose HTTP/TCP, a generic command runner, direct database access, service-manager commands, capture commands, router credentials, or Docker control. Adding one safe mutation does not grant generic process, filesystem, network, capability-lifecycle, or SQL authority.

## API framing

Each local connection sends one newline-terminated JSON request and receives one JSON response. The API version is independent from the controller binary version.

Read example:

```json
{"version":1,"id":"example","method":"status","auth":"..."}
```

Typed mutation example:

```json
{"version":1,"id":"example","method":"device.label","auth":"...","params":{"device_id":"device.example","label":"Living Room TV"}}
```

Unknown top-level fields, unexpected parameters on read methods, unknown mutation parameters, unknown methods, and API-version mismatches fail with typed errors. Requests are capped at 64 KiB and connections have a five-second deadline.

This framing is intentionally small and replaceable; the controller-side authorization and domain contracts matter more than the transport serialization choice.
