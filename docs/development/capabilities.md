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

## Current builtin

`Device Watch` is the first manifest. It requires an enrolled network scope, declares local-network access as a conditional platform prerequisite, has no deep links, and exposes only `preflight`, `enable`, `verify`, and `disable` lifecycle actions.

The manifest reserves `device-observation` and `coverage-sample` output contracts for the v0.1 data/coverage work in #10/#12. Those issues remain responsible for the actual normalized schemas and semantics.

## What remains in #9

This PR establishes the catalog and validation boundary only. Issue #9 remains open for:

- capability-instance persistence and controller API exposure;
- idempotent/cancelable lifecycle orchestration with bounded retries;
- real preflight checks for permissions/runtime/disk/network prerequisites;
- managed versus external ownership transitions;
- artifact provenance/update/uninstall handling; and
- demonstration against a real external service integration before generalizing the adapter contract.
