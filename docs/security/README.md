# Cozy SOC security design

This directory contains the maintained security-design contract for Cozy SOC.

- [Threat model](threat-model.md) — assets, adversaries, trust boundaries, abuse cases, residual risks, and review triggers.
- [Security requirements](security-requirements.md) — normative requirements with implementation owners and regression-test expectations.
- [`SECURITY.md`](../../SECURITY.md) — vulnerability reporting policy for the public repository.

The initial threat model is a **Foundation design baseline**, not a certification or a claim that unimplemented controls already exist. A requirement becomes a product guarantee only after its owning implementation issue and release tests pass.

## Maintenance rule

Security modeling is continuous. Revisit these documents whenever a change introduces or materially alters:

- a trust boundary or privileged process;
- a management listener or authentication method;
- a remote sensor, browser, or integration protocol;
- secret/credential handling;
- a managed third-party engine or update source;
- active network scanning or a write-capable network action;
- retained household data, export, backup, or diagnostics; or
- the updater, signing, installer, or service-lifecycle model.

The architecture remains the source of truth for product/process boundaries. The threat model describes how those boundaries can fail and what the implementation must enforce.