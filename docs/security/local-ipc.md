# Local controller IPC security

- **Issue:** #8
- **Status:** v0.1 implementation in progress
- **Threat-model requirements:** SEC-001–SEC-004, SEC-007–SEC-008, SEC-019, SEC-034–SEC-035

## Current transport

The controller exposes no TCP or HTTP management listener. Local clients use a Unix-domain socket inside the controller state directory.

The state directory is mode `0700`; the socket, configuration, and ephemeral authentication file are mode `0600`.

## Session authentication

Each successful controller start generates a fresh 256-bit random session secret after single-instance ownership has been established. The secret is written atomically to `controller.auth` and is removed on clean shutdown. A crashed controller may leave a stale secret, but the next successful controller start replaces it with a new value.

Clients load the secret from the private state directory and include it in each local request. Missing or incorrect values receive only a generic `unauthorized` error. Request payloads and secrets are never written to controller logs.

This secret is an **ephemeral local session credential**, not a persistent integration/user credential. Persistent credentials remain subject to the OS-backed secret-store work still required by #8.

## Kernel peer identity

On Linux, accepted Unix connections are checked with `SO_PEERCRED`; the peer UID must match the controller effective UID before request authentication is processed. Failure to obtain peer credentials fails closed.

On macOS, this slice still relies on the private socket directory plus the ephemeral session secret. Apple provides reliable UNIX-domain peer credential primitives such as `getpeereid`; adopting the final macOS client/code-identity policy remains an explicit #8 item rather than being approximated here.

## What this blocks now

- unauthenticated callers that can merely reach the socket;
- unrelated Linux users even if filesystem permissions are misconfigured broadly enough to permit a connection;
- browser-to-localhost attacks because there is no HTTP/TCP listener;
- stale auth material surviving a normal controller restart; and
- accidental credential disclosure through normal status/health responses and logging.

## Residual boundaries

This is not the completed #8 security model. In particular:

- another process running as the same OS user may be able to read the ephemeral token if it already has equivalent filesystem authority;
- macOS peer/code-signing identity validation is not yet implemented;
- persistent secrets are not yet stored in Keychain/Secret Service or a defined headless backend;
- the Tauri renderer/native-command policy is not yet implemented; and
- no privileged helper exists yet, so helper caller/scope validation remains future work before any privileged operation ships.

No mutating controller API should be added until these remaining authorization boundaries are resolved for the operation being introduced.
