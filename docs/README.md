# Cozy SOC documentation

The [project README](../README.md) and this documentation are the canonical sources
for the product's current behavior, requirements and design. The
[product roadmap](roadmap.md) defines release scope and gates. GitHub issues and
PRs track execution and discussion; they do not replace these contracts or certify
support through their open/closed status.

## Product and deployment

- [Roadmap](roadmap.md): product boundaries, release scope and acceptance gates.
- [Architecture](architecture/README.md): system and domain models, support matrix,
  resource budgets and accepted/provisional decisions.
- [Security](security/README.md): threat model and normative security requirements.
- [Integrations](integrations/README.md): ownership, compatibility and distribution policy.

## Current developer build

- [Commands](development/unified-cli.md) and [controller](development/controller.md).
- [Frontend and local web](development/frontend.md).
- [Onboarding](development/onboarding.md), [Device Watch](development/device-watch.md)
  and [coverage](development/coverage.md).
- [Network quality](development/network-quality.md),
  [gateway checks](development/interactive-gateway-check.md),
  [resolver consent](development/resolver-consent.md),
  [HTTPS checks](development/https-controller.md) and
  [retained diagnosis](development/retained-quality-diagnosis.md).
- [Local storage](development/storage.md) and
  [evidence batch format](development/evidence-batch-codec.md).

Individual documents distinguish available runtime behavior from proposed or
inactive components. An implementation contract or prototype is not a released
capability or platform-support claim.

## Validation and decisions

- [Testing](development/testing.md) defines verification scope and fixture isolation.
- [Validation gates](architecture/validation-gates.md) define evidence requirements.
- [Storage workload](development/device-watch-storage-workload.md) records the
  production resource result and its reproducible measurement boundary.
- [Batch adapter workload](development/device-watch-batch-adapter-workload.md)
  defines component footprint measurement before identity-query integration.
- [ADRs](adr/) record architectural decisions and rationale.

Feature and operating guides describe the current contract directly. Keep benchmark
reports, experimental results and decision rationale separately identified; preserve
their provenance without turning the README or feature guides into a PR diary.
When changing behavior, update the relevant canonical documents in the same change.
