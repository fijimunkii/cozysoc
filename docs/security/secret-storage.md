# Persistent secret storage

- **Issue:** #8
- **Status:** v0.1 implementation in progress
- **Threat-model requirements:** SEC-008, SEC-019, SEC-028

## Contract

Persistent credentials use the controller-owned `SecretStore` interface. Integrations receive opaque secret references, not plaintext values in configuration, URLs, process arguments, frontend storage, or diagnostic bundles.

Secret values copy their input/output buffers and implement redacting string/log representations. Reference names are restricted to a small lowercase ASCII grammar so a future integration cannot smuggle terminal control characters, paths, or command syntax into secret-store metadata.

## macOS desktop backend

The first desktop backend is the native macOS Keychain through `github.com/lexfrei/keychain` v1.2.0. Cozy SOC uses that library's native Security.framework path and `TrustCurrentApp` access mode.

Consequences:

- the secret value is never sent through `/usr/bin/security`, argv, or an environment variable;
- the controller, rather than the renderer, owns credential access;
- Keychain access is additionally tied to the controller's macOS code identity;
- production upgrades must keep a stable signing identity/Team ID; and
- unsigned/ad-hoc developer rebuilds may lose access to previously stored native items. Development must not switch to the library's CLI fallback to hide that behavior.

The fixed Keychain service namespace is `com.cozysoc.credentials.v1`. Accounts are Cozy SOC-owned opaque references rather than raw endpoint URLs or passwords.

The dependency is BSD-3-Clause and cgo-free. Its v1.2.0 macOS implementation uses `purego` to call Security.framework. Binary release notices remain part of the cross-cutting release work in #28.

## Headless policy

`NewHeadless` intentionally fails closed today. A Linux hub must not silently reuse a desktop login keyring, depend on an unlocked graphical session, or fall back to an unencrypted file because a Secret Service daemon is absent.

Issue #15 must define the headless machine-bound backend and its key lifecycle before persistent hub credentials are enabled. That design must document provisioning, restart/reboot availability, backup/restore behavior, rotation/revocation, filesystem/root residual risk, and recovery when the key source is unavailable.

## Current limitations and release evidence

This package establishes the storage boundary but no integration stores credentials yet. Before the first credential-bearing capability ships:

1. exercise native Keychain set/get/update/delete on a signed reference macOS build;
2. verify secrets do not appear in process arguments, environment, logs, diagnostics, or config files;
3. verify locked/denied/unavailable states become safe user-visible partial capability;
4. verify credential replacement and revocation; and
5. keep a regression fixture in #29's security test gates.

A successful cross-compile is not Keychain runtime evidence.
