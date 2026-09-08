# Cozy SOC integration strategy

This directory records the evidence used to decide which specialist tools Cozy SOC connects to, manages, or deliberately leaves out of the default product.

- [`evaluation-2026-09-08.md`](evaluation-2026-09-08.md) — dated Foundation research snapshot for issue #6.
- [`../adr/0007-integration-engine-strategy.md`](../adr/0007-integration-engine-strategy.md) — accepted product/architecture decision derived from that research.

The important distinction is **product support versus upstream availability**. A tool being installable, open source, or supported by its own project does not make it a Cozy SOC dependency or support claim.

## Ownership modes

Cozy SOC uses three explicit integration ownership modes:

1. **External** — the user owns lifecycle and configuration. Cozy SOC connects with the minimum required authority and does not restart, upgrade, rewrite, or uninstall the service.
2. **Managed** — Cozy SOC owns a specifically tested deployment and its lifecycle. Managed status requires an explicit version, provenance/update path, resource budget, recovery behavior, and distribution/compliance review.
3. **User-supplied executable** — Cozy SOC may integrate with a separately installed tool, but does not redistribute it. Version detection and capability checks remain mandatory.

A component can support more than one mode, but changing ownership is an explicit state transition rather than an incidental side effect of connecting to it.

## Review rule

The Foundation snapshot is dated. Exact supported versions are selected and tested by the implementation issue; they are not automatically advanced to whatever upstream calls latest. Re-evaluate an integration when its license, API contract, packaging model, privileges, security posture, or maintenance status materially changes.
