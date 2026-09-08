# Cozy SOC security requirements

- **Status:** Foundation baseline
- **Date:** 2026-09-08
- **Primary issue:** #4
- **Threat model:** [`threat-model.md`](threat-model.md)

These are normative design requirements. They become release guarantees only after the owning implementation and cross-cutting tests pass.

`MUST` and `MUST NOT` identify release-blocking behavior for a capability that is actually shipped. Requirements for components that do not yet exist are dormant until that component is introduced.

## Controller and local access

### SEC-001 — Controller-side authorization

All control-plane authorization MUST be enforced by the controller or a narrower privileged component. Frontend state, generated TypeScript types, UI visibility, or a sensor's claimed role MUST NOT be treated as authorization.

**Owners:** #7, #8, #15, #27

**Regression:** #29

### SEC-002 — No ambient unauthenticated management endpoint

The default desktop deployment MUST NOT expose an unauthenticated HTTP/TCP management API on loopback, LAN, wildcard, or public interfaces.

**Owners:** #7, #8

**Regression:** #29

### SEC-003 — Local caller identity

Local IPC MUST restrict access to the intended OS principal(s) and MUST reject unrelated local users/processes. Endpoint permissions alone MAY be part of the control, but the implementation MUST document the effective caller-identity check.

**Owners:** #8

**Regression:** wrong-user/process tests in #29

### SEC-004 — Browser-to-local protections

If any HTTP bridge is introduced, state-changing requests MUST require authentication and browser-request defenses appropriate to the transport, including explicit Host/origin policy and CSRF protection. Missing/ambiguous security headers MUST fail closed for sensitive operations rather than granting ambient browser authority.

**Owners:** #8, #15

**Regression:** localhost/rebinding/origin/CSRF tests in #29

## Renderer and native shell

### SEC-005 — Minimal renderer capability

Renderer code MUST NOT receive generic process execution, unrestricted filesystem access, service-manager authority, packet-capture authority, router credentials, direct database access, or a Docker/container-management socket.

**Owners:** #8, #13, #28

**Regression:** capability/command-surface review and adversarial invocation tests in #29

### SEC-006 — No privileged remote content

The privileged application WebView MUST NOT load arbitrary remote application HTML/JavaScript. Content-security policy and shell configuration MUST keep remote content from inheriting native privileges.

**Owners:** #8, #13

**Regression:** navigation/CSP tests in #29

### SEC-007 — Hostile input handling

Hostnames, SSIDs, URLs, protocol fields, engine logs, router data, endpoint data, sensor data, filenames, and other imported strings MUST be treated as hostile. They MUST NOT be interpolated into executable commands, privileged paths, HTML, SQL, or unsafe navigation contexts without appropriate structured APIs/encoding/validation.

**Owners:** #8, #10, #13, #15, #16, #17, #18, #19, #22, #26

**Regression:** injection corpus in #29

### SEC-008 — Input and workload bounds

Every untrusted ingress MUST define reasonable record-size, collection-size, parsing-time, concurrency, queue, retry, and rate bounds. Unsupported or malformed records MUST be isolated without unbounded retry or memory growth.

**Owners:** #7, #9, #10, #15, #19, #22, #26

**Regression:** oversized/malformed/flood cases in #29

## Privileged helpers

### SEC-009 — Narrow helper API

A privileged helper MUST expose a typed allowlist of operations. It MUST NOT expose a generic shell, arbitrary command runner, unrestricted file writer, generic package manager, or generic router command proxy.

**Owners:** #8, #28

**Regression:** command-surface and malformed-argument tests in #29

### SEC-010 — Independent privileged validation

The helper MUST authenticate its expected caller and independently validate security-sensitive arguments, canonical targets, preconditions, and requested scope. It MUST NOT trust the controller merely because the controller produced the request.

**Owners:** #8, #27

**Regression:** path/target/scope/caller-negative tests in #29

## Integration endpoints and ownership

### SEC-011 — Approved authority binding

Credentials and privileged integration requests MUST be bound to the explicitly enrolled endpoint authority. User-controlled or remotely supplied redirects MUST NOT cause credentials to be forwarded to a different authority.

**Owners:** #16, #17, #26

**Regression:** redirect/credential-forwarding tests in #29

### SEC-012 — Scheme, DNS, and destination validation

Integration endpoints MUST use an allowlisted scheme appropriate to the integration. DNS/address changes that could change the security boundary MUST be revalidated; the implementation MUST account for IPv4 and IPv6. An endpoint whose identity changes unexpectedly MUST degrade or require re-approval rather than silently broadening access.

**Owners:** #16, #17, #26

**Regression:** DNS-rebinding/address-family/redirect tests in #29

### SEC-013 — TLS trust

TLS verification MUST NOT be globally disabled. Self-signed/local certificates MAY be supported only through an explicit trust-enrollment flow scoped to the approved endpoint.

**Owners:** #15, #16, #17, #26

**Regression:** wrong-cert/expired-cert/untrusted-cert tests in #29

### SEC-014 — External ownership preservation

Connecting an externally managed service MUST default to the minimum required authority and MUST NOT permit restart, upgrade, configuration rewrite, or uninstall unless the user explicitly transitions that capability to a supported managed mode.

**Owners:** #9, #16, #17, #26

**Regression:** ownership/lifecycle tests in #29

## Remote sensors

### SEC-015 — Strong sensor identity

Remote sensors MUST use unique, revocable enrolled identities over authenticated encrypted transport. Pairing/bootstrap credentials MUST be short-lived or single-use where practical and MUST NOT become permanent shared secrets for all sensors.

**Owners:** #15

**Regression:** expired/reused/revoked/mismatched identity tests in #29

### SEC-016 — Sensor scope and authority

A sensor identity MUST be bound to its declared installation/network/capability scope. Sensor-originated messages MUST NOT directly invoke control-plane configuration or network actions.

**Owners:** #15, #27

**Regression:** out-of-scope/control-message tests in #29

### SEC-017 — Replay, order, and time handling

Sensor/event ingestion MUST detect or safely handle duplicate delivery, replay, source restart, out-of-order events, and clock skew. Historical/replayed events MUST NOT refresh current sensor health or coverage.

**Owners:** #10, #12, #15

**Regression:** replay/order/clock fixtures in #29

## Local data, secrets, and privacy

### SEC-018 — Controller-only database ownership

Only the controller may open the Cozy SOC database in normal operation. UI, engines, sensors, and third-party pages MUST use controller contracts. Queries MUST use structured/parameterized database APIs; file permissions and migrations MUST be appropriate for sensitive local data.

**Owners:** #10

**Regression:** ownership/migration/query tests in #29

### SEC-019 — Secret isolation and redaction

Credentials, private keys, enrollment secrets, signing material, and reusable tokens MUST NOT be stored in frontend storage, embedded in URLs, passed in ordinary process arguments when avoidable, or emitted by default logs/diagnostics. Platform credential stores or an explicitly designed protected headless secret store MUST be used where applicable.

**Owners:** #8, #15, #16, #17, #26, #28, #30

**Regression:** redaction/secret-scanning tests in #29

### SEC-020 — Data minimization and bounded retention

Collection and retention MUST be capability-specific and bounded. Full packet payload retention MUST remain off by default. Storage pressure/expiry MUST be visible and MUST NOT silently turn old evidence into current coverage.

**Owners:** #10, #19, #22, #23, #26, #30

**Regression:** quota/expiry/low-disk tests in #29

### SEC-021 — Safe exports and diagnostics

Exports, support bundles, and diagnostic captures MUST be explicit, bounded, previewable where practical, and redacted by default. They MUST NOT include reusable secrets or unnecessary household identifiers.

**Owners:** #30

**Regression:** diagnostic/export redaction corpus in #29

## Supply chain and updates

### SEC-022 — Authenticated artifact provenance

Release, helper, managed-engine, rule/feed, and updater artifacts MUST follow a documented provenance/integrity path appropriate to the source. Production signing material MUST NOT be exposed to untrusted pull-request workflows. Unexpected privilege expansion or unauthorized downgrade MUST be rejected or require an explicit trusted recovery path.

A checksum delivered from the same unauthenticated channel as an artifact MUST NOT be described as a signature or independent provenance proof.

**Owners:** #9, #20, #28

**Regression:** tamper/downgrade/wrong-platform/provenance tests in #29

## Authorized network scope and actions

### SEC-023 — Active operations stay in enrolled scope

Active discovery/assessment MUST execute only against an explicitly authorized `NetworkScope`. Route/interface/VPN/address changes that can alter scope MUST be revalidated immediately before or during the operation; cancellation MUST be possible.

**Owners:** #11, #25

**Regression:** VPN/route/DNS/IPv6 scope-escape tests in #29

### SEC-024 — Network changes are transactional and recoverable

Write-capable actions MUST separate preview, authorization, application, verification, and rollback/expiry state. Targets and control-point preconditions MUST be revalidated at execution time. Partial success MUST be represented explicitly.

**Owners:** #16, #27

**Regression:** partial apply/crash/reboot/concurrent-change/rollback tests in #29

### SEC-025 — Detection does not grant enforcement authority

A finding, IDS rule, anomaly detector, external engine, AI/LLM output, or remote sensor MUST NOT automatically authorize a network-changing action. An action requires an available write-capable control point and explicit policy/user authorization.

**Owners:** #18, #27

**Regression:** false-positive/action-boundary tests in #29

## User-visible privacy and evidence

### SEC-026 — Notification minimization

Lock-screen and external notifications MUST minimize household activity details by default. Sensitive DNS/destination/file/path evidence SHOULD require opening the authenticated UI.

**Owners:** #18, #30

**Regression:** notification content tests in #29

### SEC-027 — Coverage requires fresh evidence

Runtime/process health, historical observations, and configured scope MUST remain distinct from verified current observation. Old/replayed data MUST NOT keep a disconnected or blind sensor green.

**Owners:** #9, #12, #19, #22

**Regression:** stale/quiet/missing-data/drop tests in #29

### SEC-028 — Resource exhaustion containment

Controller queues, retries, storage, event ingestion, logs, and per-source work MUST be bounded. On overload the system MUST preserve an explicit degraded/data-loss signal rather than hanging, filling disk indefinitely, or silently dropping evidence while reporting healthy coverage.

**Owners:** #7, #9, #10, #12, #19, #22, #26

**Regression:** sustained flood/low-disk/backpressure tests in #29

### SEC-029 — Engines are untrusted data producers

Managed/external engines and parsers MUST be treated as potentially compromised. They MUST receive only the credentials/host/network privileges needed for their capability and MUST NOT gain controller, secret-store, privileged-helper, or router-write authority by default. Engine output MUST be schema/size/rate validated before it affects controller state.

**Owners:** #9, #19, #22, #24, #26

**Regression:** malformed/hostile-engine tests in #29

### SEC-030 — Security-relevant state is auditable

The controller MUST record a durable audit event for security-relevant trust/configuration/action transitions without recording reusable secrets. Audit history MUST distinguish requested, applied, effective, failed, expired, and rolled-back states where applicable.

**Owners:** #10, #15, #16, #18, #27, #30

**Regression:** audit completeness/state-transition tests in #29

## Browser management, navigation, and backups

### SEC-031 — Hub browser management is explicit and hardened

Browser management of a headless hub MUST be disabled or non-listening until explicitly configured. When enabled it MUST use authenticated encrypted transport, safe session handling, explicit bind/origin policy, and CSRF protections for state-changing operations. Cozy SOC MUST NOT require public port forwarding for normal use.

**Owners:** #8, #15

**Regression:** unauthenticated LAN/WAN/origin/session tests in #29

### SEC-032 — Deep links are non-authoritative navigation

Deep links to third-party/admin tools MUST use approved schemes/origins, MUST NOT embed reusable credentials, and MUST NOT grant the destination Cozy SOC native/controller privileges. Context such as device/time identifiers MUST be treated as navigation hints, not authorization.

**Owners:** #13, #16, #17, #26

**Regression:** scheme/origin/credential/context tests in #29

### SEC-033 — Backup trust does not clone authority

Ordinary configuration/history backups MUST NOT silently clone or revive revoked credentials, sensor identities, router-write authority, or signing material. Restores to a new host MUST require explicit reauthorization/re-enrollment for trust material that should not be portable.

**Owners:** #15, #30

**Regression:** backup/restore identity tests in #29

## Failure policy and test evidence

### SEC-034 — Fail closed on security-critical uncertainty

A security-sensitive operation that cannot confidently establish caller identity, target identity, authorization scope, endpoint authority, or required preconditions MUST refuse or require reauthorization. The product MAY degrade observation coverage, but MUST NOT silently broaden control authority to preserve convenience.

**Owners:** #8, #11, #15, #16, #17, #25, #27

**Regression:** ambiguity/failure-path tests in #29

### SEC-035 — Negative paths are release evidence

Security requirements are not satisfied by a successful happy-path demo alone. Applicable release evidence MUST include negative/adversarial cases for caller identity, malformed input, stale/replayed data, denied permission, endpoint/certificate changes, scope changes, overload, partial network actions, update tampering, and cleanup/recovery.

**Owner:** #29, with each implementation issue providing fixtures/hardware scenarios.

## Release mapping

| Release area | Minimum applicable security requirements |
| --- | --- |
| Foundation | Threat model itself; architecture must preserve boundaries in SEC-001, SEC-005, SEC-009, SEC-014, SEC-025, SEC-029 |
| v0.1 desktop alpha | SEC-001–SEC-010, SEC-018–SEC-020, SEC-022–SEC-023, SEC-027–SEC-030, SEC-034–SEC-035 |
| v0.2 hub / DNS / router / incidents | v0.1 plus SEC-011–SEC-017, SEC-024–SEC-026, SEC-031–SEC-033 |
| v0.3 Traffic Watch | SEC-007–SEC-008, SEC-017, SEC-020, SEC-022, SEC-027–SEC-030, SEC-035 plus v0.2 base |
| v0.4 Wireless Watch | SEC-007–SEC-008, SEC-020–SEC-021, SEC-027–SEC-030, SEC-035 plus v0.2 base |
| v0.5 optional response/posture | All requirements applicable to the enabled module, especially SEC-023–SEC-025 and SEC-034–SEC-035 |

## Change control

A future PR that intentionally weakens or removes a requirement MUST call out the security tradeoff explicitly and update the threat model/requirement status. It must not be weakened silently as an implementation convenience.
