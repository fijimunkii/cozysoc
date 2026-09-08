# ADR 0001: License Cozy SOC under MIT

- **Status:** Accepted for the initial first-party repository
- **Date:** 2026-09-08
- **Decision owners:** Cozy SOC maintainers

## Context

Cozy SOC is intended to be a local-first security product that integrates with multiple specialist tools. Some integrations may be external services, some may be separately managed processes, and future packaging decisions may involve software with materially different license obligations.

The first-party repository needs a clear license before implementation begins. That choice should keep early contribution and reuse simple without pretending that the first-party license determines whether third-party engines, drivers, rules, or data feeds may be redistributed.

## Options considered

### MIT

Advantages:

- short and easy for contributors and downstream users to understand;
- broadly compatible with permissive and copyleft ecosystems;
- minimal notice requirements; and
- does not unnecessarily constrain future commercial or self-hosted distribution models.

Tradeoff:

- unlike Apache-2.0, MIT does not contain an explicit patent-license grant and termination framework.

### Apache License 2.0

Advantages:

- permissive;
- explicit patent grant and patent-termination terms; and
- clear contribution and notice language.

Tradeoffs:

- more complex than MIT for a small initial project;
- adds NOTICE-related considerations when applicable; and
- its patent provisions do not remove the need to review third-party integration and distribution terms independently.

### BSD-style permissive licenses

These would also be workable, but offer no material advantage for the initial project over MIT while being less aligned with the project's preference for a very familiar, minimal permissive license.

### GPL/AGPL-family copyleft licenses

Strong copyleft can be appropriate for self-hosted infrastructure, especially when preserving downstream source availability is a primary goal. It is not selected for the initial first-party core because Cozy SOC is expected to interact with desktop shells, native helpers, multiple separately licensed engines, and potentially commercial packaging. We should not impose that architectural policy before the integration model is validated.

## Decision

Use the **MIT License** for Cozy SOC first-party code and documentation.

This decision applies only to work owned by this repository. Every third-party engine, library, driver, detection feed, rule set, asset, and bundled artifact must be reviewed on its own terms before integration or redistribution. Containerization, sidecar execution, or API access must not be treated as a blanket license exemption.

## Consequences

- The repository includes the standard MIT text in `LICENSE`.
- New first-party source files do not need individual license headers unless a later policy requires them.
- Dependency and redistribution review remains a separate P0 deliverable under issue #6.
- If Cozy SOC later develops meaningful patent-sensitive functionality, accepts substantial corporate contributions, or changes its distribution model, maintainers should explicitly revisit MIT versus Apache-2.0 rather than silently relicensing existing contributions.