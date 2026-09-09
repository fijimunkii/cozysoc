# Capability catalog v1

Issue #9 introduces a declarative catalog for user-facing capabilities such as Device Watch, DNS Protection, Traffic Watch, and Wireless Watch.

The catalog is deliberately **not a plugin execution format**. A manifest describes a vetted capability that is implemented by first-party code or a separately reviewed adapter. It cannot contain shell commands, executable paths, container arguments, downloaded scripts, or arbitrary lifecycle hooks.

## Manifest contract

Schema version `1` records:

- stable capability ID, display name, summary, and intended release line;
- supported ownership modes (`builtin`, `external`, `managed-local`, `managed-remote`);
- explicit OS/architecture targets and their evidence level (`candidate`, `planned`, `tested`, `limited`);
- required inputs, privilege/consent prerequisites, and dependencies;
- a small typed configuration schema where secret-bearing fields use `secret-ref` rather than raw credentials;
- measured or explicitly **unmeasured** resource budget status;
- provenance, license, source, and version policy;
- health verification signals that remain separate from process state;
- normalized output contract identifiers;
- optional deep-link contexts; and
- supported lifecycle actions.

Manifest input is capped at 64 KiB, unknown JSON fields are rejected, duplicate identifiers are rejected, and builtin capabilities cannot declare installer/process/upgrade actions. Adding an executable-shaped field such as `command` therefore fails decoding rather than becoming an implicit extension point.

## Support honesty

Catalog target matching means only that the manifest contains an entry for an OS/architecture/version. The target also carries its evidence level. A `candidate` match is **not** converted into a `tested` support claim.

The initial `device-watch` manifest therefore records macOS 13+ Apple Silicon as `candidate`, matching the architecture support matrix. Its resource profile remains `unmeasured` until the real Foundation measurements are recorded.

## State model

Capability state has three independent dimensions:

1. **Desired** — what the user asked for (`enabled` or `disabled`).
2. **Process** — whether a separate process is applicable/stopped/starting/running/failed.
3. **Verification** — whether expected evidence is unverified/verifying/verified/degraded/stale.

This prevents `enabled` or `running` from becoming a synonym for observed coverage. Device Watch is builtin, so its independent process state is `not-applicable`; it still requires explicit verification signals before coverage can be represented as verified.

## Configured instances

Controller config schema `2` persists only durable capability intent: the capability ID, selected allowed ownership mode, desired state, and typed configuration values. Secret-bearing fields accept only opaque `secret-ref` identifiers; raw secret bytes remain in the secret store.

A valid schema `1` controller config is migrated atomically to schema `2`. Unknown config fields, duplicate capability entries, oversized configuration, invalid ownership, unknown settings, and wrong setting types fail closed rather than being ignored.

Runtime process and verification states are **not** restored from config. Every controller start reconstructs them from current runtime/evidence, beginning unverified. Historical operational and verification state belongs in the bounded controller-owned persistence introduced by #10 rather than in durable configuration.

The controller now has a serialized configuration manager for explicitly reviewed live intent updates. `config.json` remains the durable source of truth; in-memory capability state is updated only after the atomic config write succeeds. Proposed configuration can be preflighted without mutating current desired state.

The authenticated local API exposes the read-only `capabilities.list` method. It returns catalog metadata, whether an instance was explicitly configured, selected ownership, and desired/process/verification state. It deliberately does not return configured values or secret references.

## Lifecycle execution contract

Lifecycle execution is a controller reconciliation boundary. Drivers are compiled-in Go implementations registered by capability ID; a manifest cannot supply executable code, a command line, a dynamic hook, or an environment block.

Before an action executes, the lifecycle engine checks that:

- the action is declared by the capability manifest;
- the selected ownership mode permits it;
- the action agrees with the already-persisted desired state; and
- the registered driver's action-specific preflight has no unresolved blocking checks.

Preflight is side-effect-free and returns bounded typed checks (`pass`, `fail`, or `unknown`). A blocked preflight prevents the mutating driver call. Retryable driver errors use a controller-owned bounded retry policy; cancellation interrupts both calls and retry waits. Operations and live configuration changes for the same capability share a per-capability lock so execution cannot race a stale configuration snapshot.

External ownership is protected independently of the manifest: external services cannot be installed, started, stopped, upgraded, or uninstalled through this engine. A future explicit ownership transition must occur before Cozy SOC gains those management rights.

Process state and verification remain separate after execution. Any mutating lifecycle action invalidates prior verification. `verify` only becomes `verified` when **every** verification signal declared by the manifest is reported fresh; missing signals remain unverified, stale signals become stale, and failed signals become degraded. A running producer by itself can never produce verified coverage.

## First real driver: Device Watch

Device Watch is the first capability with a production lifecycle driver rather than only a catalog contract. The driver owns its passive runtime and implements `preflight`, `enable`, `verify`, and `disable`.

The public `device-watch.enable` and `device-watch.disable` methods are deliberately capability-specific. Cozy SOC does **not** expose a generic `capability.run` or arbitrary lifecycle RPC.

Enablement requires exactly one enrolled Device Watch network scope. A proposed enabled configuration is preflighted before durable intent changes. If preflight passes, the controller records the requested transition, writes `config.json` atomically, updates the lifecycle engine's configuration view, and executes normal lifecycle enable. If runtime enable fails, the runtime is stopped and the previous durable configuration is restored.

Disable is safety-biased. The desired state is durably changed to `disabled` before runtime stop executes, and disable preflight never depends on the enrolled network still being reachable/present. If runtime stop fails, the durable state stays disabled so a restart cannot resurrect monitoring the user asked to stop; an emergency stop is attempted as containment.

Device Watch's driver can establish that the enrolled network scope is current, but `observation-freshness` remains missing until #12 evaluates actual fresh coverage evidence. Therefore successful enablement remains `unverified` rather than becoming a green coverage claim.

The manifest reserves `device-observation` and `coverage-sample` output contracts for the v0.1 data/coverage work in #10/#12. Those issues remain responsible for the normalized schemas and current-evidence semantics.

## What remains in #9

The catalog, configured-instance model, live configuration reconciliation, internal lifecycle engine, and first real Device Watch driver are established. Issue #9 remains open for:

- #12-backed verification using actual fresh coverage evidence;
- safe managed-versus-external ownership transitions;
- artifact provenance/update/uninstall behavior for capabilities that distribute artifacts; and
- demonstration against a real external service integration before generalizing managed service lifecycle behavior.
