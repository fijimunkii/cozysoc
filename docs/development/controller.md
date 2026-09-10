# v0.1 controller bootstrap

Issue #7 introduced the first production-oriented Cozy SOC process: an independent Go controller with a small typed local API. Issue #84 now consolidates the user-facing command entrypoint under `cozysoc` without changing that controller's independent lifetime or authority boundary.

## Current controller slice

`cozysoc serve` provides:

- a versioned configuration file in a private state directory;
- an independent long-running process with graceful signal shutdown;
- versioned local read methods including `status`, `health`, `capabilities.list`, `devices.list`, `networks.list`, and `device-watch.coverage`;
- explicitly allowlisted, controller-authorized mutations (`device.label`, `network.enroll`, `device-watch.enable`, and `device-watch.disable`) rather than a generic write surface;
- a permissioned Unix-domain socket rather than a TCP/localhost controller listener;
- single-instance protection through the local socket;
- bounded request size, deadline, and concurrent-client count;
- strict top-level request decoding and typed per-method parameters;
- atomic live capability-intent persistence through `config.json`;
- JSON structured logs that do not log request payloads;
- a health ticker that records long scheduling/sleep gaps; and
- typed CLI clients in the same `cozysoc` executable using the same protected IPC path.

macOS service registration, reboot/crash lifecycle, final Tauri packaging, and real sleep/resume evidence remain gated by #5/#56.

## Run locally

Start the controller:

```bash
go run ./cmd/cozysoc serve --state-dir /tmp/cozysoc-dev
```

From another terminal:

```bash
go run ./cmd/cozysoc status --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc health --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc capabilities --state-dir /tmp/cozysoc-dev
go run ./cmd/cozysoc networks --state-dir /tmp/cozysoc-dev
```

`networks` lists local interfaces that are eligible to become an explicit Device Watch authorization scope. Candidate enumeration reads only local interface/address metadata; it does not ping, resolve, scan, or otherwise probe the network.

To authorize one of those interfaces:

```bash
go run ./cmd/cozysoc network-enroll --state-dir /tmp/cozysoc-dev INTERFACE
```

Enrollment captures the interface name, interface index, and current usable IPv4/IPv6 prefixes at execution time. Loopback, down, and point-to-point/tunnel interfaces fail closed. v0.1 permits one active Device Watch network scope: re-enrolling the exact same binding is an idempotent no-op, while trying to enroll a different network returns a conflict instead of silently replacing authorization.

**Enrollment does not enable Device Watch.** To request passive observation after enrollment:

```bash
go run ./cmd/cozysoc device-watch-enable --state-dir /tmp/cozysoc-dev
```

To inspect detailed current Device Watch evidence, source health, and known blind spots:

```bash
go run ./cmd/cozysoc device-watch-coverage --state-dir /tmp/cozysoc-dev
```

To inspect only the capability-independent coverage envelope used by the UI:

```bash
go run ./cmd/cozysoc coverage --state-dir /tmp/cozysoc-dev
```

To stop Device Watch:

```bash
go run ./cmd/cozysoc device-watch-disable --state-dir /tmp/cozysoc-dev
```

Enable preflights the current platform and enrolled scope before writing enabled intent. Disable does not require the network to remain available. Enablement by itself remains unverified; the controller independently advances verification only from current retained coverage evidence.

`device-watch-coverage` is read-only and parameterless. It reports the current durable Device Watch scope only; callers cannot request another scope, sensor, arbitrary historical time, or raw stored evidence. The response separates aggregate state from ARP/NDP source state and includes curated blind spots/next steps. `coverage` projects only the nested shared coverage contract plus its evaluation time; it does not expose Device-Watch-specific queue/storage/operational details. See `docs/development/coverage.md` for the contract.

Once Device Watch is enabled and a device exists in its scope, its user label can be changed through the narrow mutation surface:

```bash
go run ./cmd/cozysoc device-label --state-dir /tmp/cozysoc-dev DEVICE_ID "Living Room TV"
```

An empty label argument clears the user label.

The controller creates the state directory mode `0700`, `config.json` and the SQLite database mode `0600`, and `controller.sock` / `controller.auth` mode `0600` while the service is running.

## Local web UI

Build the shared frontend first:

```bash
cd ui
npm ci --ignore-scripts --no-audit --no-fund
npm run build
cd ..
```

Then, while `cozysoc serve` remains running independently:

```bash
go run ./cmd/cozysoc web --state-dir /tmp/cozysoc-dev --ui-dir ui/dist
```

`cozysoc web` prints an authenticated loopback URL. Open that exact URL so the browser can exchange its one-time fragment bootstrap for a separate HttpOnly local web-session cookie. The browser session is **not** the controller UDS session secret.

The web mode currently exposes only a narrowly typed, authenticated read-only coverage endpoint. It binds to a literal loopback IP, enforces exact Host/origin rules, has no generic controller method proxy, and does not start, stop, supervise, or own the controller. Stopping the web process leaves `cozysoc serve` running.

`cozysoc dev`, which may orchestrate controller + web together for developer convenience, is intentionally a follow-up and is not production lifecycle evidence.

## Security boundary

The local controller API is not a generic controller RPC or database API. Write operations must be individually defined, authenticated, authorized, bounded, and audited.

`network.enroll`:

- accepts one validated local interface name rather than arbitrary prefixes or a caller-supplied scope;
- re-reads the interface at execution time and derives the exact binding itself;
- rejects loopback, point-to-point/tunnel, down, or otherwise unusable interfaces;
- serializes concurrent enrollment attempts and refuses silent replacement of a different active scope;
- commits the new authorization scope and durable `network-scope-enroll` audit event in one SQLite transaction; and
- does not start discovery, change routes, change DNS, or send network packets.

Device Watch lifecycle control:

- exposes only two capability-specific, parameterless operations; there is no generic lifecycle RPC;
- derives its `network_scope_id` from the single enrolled authorization scope rather than caller input;
- preflights a proposed enabled configuration before durable intent changes;
- records a durable requested transition before writing enabled/disabled intent;
- uses the same compiled-in lifecycle driver for live operations and startup reconciliation;
- compensates failed enable by stopping runtime side effects and restoring previous durable intent; and
- persists disabled intent before stop, so a failed disable or restart cannot silently resurrect monitoring.

Device Watch coverage detail:

- is an authenticated UDS read only;
- accepts no parameters and cannot widen scope;
- strictly validates retained coverage evidence before projecting it;
- never returns raw evidence JSON or stored free-form limitation text;
- reports ARP and NDP source state independently without inventing a permission diagnosis; and
- preserves explicit blind spots including no whole-network traffic visibility.

`device.label`:

- is authenticated through the same per-controller session secret and OS peer-identity checks as reads;
- accepts strict typed parameters only;
- is pinned to the current durable Device Watch scope;
- independently revalidates the target's retained scope evidence in storage;
- validates the user label before persistence; and
- commits the device change and durable audit event in one SQLite transaction.

**Controller mode (`cozysoc serve`) does not expose HTTP/TCP management.** Its management boundary remains the authenticated Unix socket. **Web mode (`cozysoc web`) is a different process** with a narrowly scoped loopback HTTP surface and its own ephemeral browser session. The web process receives no generic command runner, direct database access, service-manager commands, capture commands, router credentials, Docker control, or general controller executor.

## API framing

Each local UDS connection sends one newline-terminated JSON request and receives one JSON response. The API version is independent from the `cozysoc` binary version.

Read example:

```json
{"version":1,"id":"example","method":"networks.list","auth":"..."}
```

Coverage read example:

```json
{"version":1,"id":"example","method":"device-watch.coverage","auth":"..."}
```

Typed enrollment example:

```json
{"version":1,"id":"example","method":"network.enroll","auth":"...","params":{"interface_name":"en0"}}
```

Parameterless Device Watch enable example:

```json
{"version":1,"id":"example","method":"device-watch.enable","auth":"..."}
```

Typed label example:

```json
{"version":1,"id":"example","method":"device.label","auth":"...","params":{"device_id":"device.example","label":"Living Room TV"}}
```

Unknown top-level fields, unexpected parameters on parameterless/read methods, unknown mutation parameters, unknown methods, and API-version mismatches fail with typed errors. Requests are capped at 64 KiB and connections have a five-second deadline.

This framing is intentionally small and replaceable; the controller-side authorization and domain contracts matter more than the transport serialization choice.
