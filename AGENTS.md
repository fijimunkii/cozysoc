# AGENTS.md

These instructions apply to automated and human-assisted coding agents working in this repository.

## Source of truth

- The canonical product roadmap is GitHub issue #1.
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
- Run `./scripts/check-repo.sh` plus all stack-specific checks introduced by the affected code.

## Review discipline

Before recommending merge:

1. inspect the final diff;
2. verify required CI is for the exact head commit;
3. read inline review threads, not only top-level review state;
4. address substantive feedback and add tests where appropriate; and
5. leave no unresolved substantive review threads.

Do not merge on behalf of the maintainer unless the user explicitly asks for it.