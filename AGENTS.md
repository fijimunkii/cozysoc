# AGENTS.md

These instructions apply to automated and human-assisted coding agents working in this repository.

## Source of truth

- README.md and docs/ are the canonical sources for current product behavior, requirements, architecture, support and release gates. The product roadmap is docs/roadmap.md.
- GitHub issue #1 coordinates the execution backlog; issues and PRs do not replace product documentation or establish support through completion status.
- Maintain documentation as current-state reference material, not an implementation diary. Update the relevant contract when behavior changes; keep dated benchmark evidence and ADR rationale separately identified.
- Work from a scoped issue. Respect its dependencies, acceptance criteria, and explicit non-goals.
- Do not turn roadmap candidates into committed dependencies without the relevant P0 decision.

## Product invariants

- Discovery, observation, detection, and enforcement are different capabilities.
- Never present process health or zero alerts as proof of full network coverage.
- Keep the product local-first; do not add mandatory cloud services or telemetry without an explicit roadmap decision.
- Active checks require explicitly authorized scope.
- Network-changing actions require deliberate consent, verification, auditability, and a tested recovery path.
- Treat all network-derived and integration-derived values as untrusted input.
- Do not expose a generic root shell, unrestricted Docker socket, or arbitrary process execution through the UI/controller boundary.
- Prefer narrow integrations with maintained specialist tools over reimplementing engines or creating broad plugin systems prematurely.
- Do not commit real credentials, private keys, private packet captures, browsing history, or unnecessary household-identifying data.

## Change discipline

- Keep PRs focused and linked to one or a small set of closely related issues.
- Use conventional commit subjects and a conventional PR title.
- Add regression coverage for substantive bug or security fixes.
- Update support/coverage documentation whenever observed behavior, privileges, or prerequisites change.
- Run `bash scripts/check-repo.sh` plus all stack-specific checks introduced by the affected code.

## Review discipline

Before recommending merge:

1. inspect the final diff;
2. verify required CI is for the exact head commit;
3. read inline review threads, not only top-level review state;
4. address substantive feedback and add tests where appropriate; and
5. leave no unresolved substantive review threads.

Do not merge on behalf of the maintainer unless the user explicitly asks for it.
