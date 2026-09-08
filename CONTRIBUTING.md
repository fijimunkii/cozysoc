# Contributing to Cozy SOC

Thanks for helping build Cozy SOC.

The project is intentionally starting with a small, reviewable foundation. Please prefer focused changes tied to an existing GitHub issue over broad speculative infrastructure.

## Before you start

1. Read the canonical roadmap in [issue #1](https://github.com/fijimunkii/cozysoc/issues/1).
2. Pick or open a scoped issue before substantial implementation work.
3. Confirm that dependencies and architectural assumptions have been accepted when the issue depends on a P0 decision.
4. Read [SECURITY.md](SECURITY.md) before sharing logs, captures, credentials, or household-network details.

For large changes, use the issue discussion to align scope before writing the implementation. Small fixes can proceed directly when the intent is clear.

## Branches

Use a short descriptive branch name, for example:

- `feat/device-watch`
- `fix/coverage-freshness`
- `docs/packet-visibility`
- `chore/bootstrap-repository`

## Conventional commits

Every commit subject and pull-request title must use this form:

```text
<type>(optional-scope): short imperative summary
```

Allowed types:

- `feat` — user-visible capability
- `fix` — bug fix
- `docs` — documentation only
- `style` — formatting with no behavioral change
- `refactor` — internal restructuring with no intended behavior change
- `perf` — performance improvement
- `test` — tests only
- `build` — build or packaging changes
- `ci` — CI changes
- `chore` — maintenance not covered above
- `revert` — revert of an earlier change

Breaking changes may use `!`, for example `feat(api)!: revise sensor enrollment`.

Good examples:

```text
feat(discovery): track device first seen time
fix(coverage): mark sleeping sensors stale
docs: explain switched network visibility
ci: validate pull request titles
```

Avoid vague subjects such as `updates`, `fix stuff`, or `WIP`.

## Local checks

Run the repository checks before opening or updating a pull request:

```bash
bash scripts/check-repo.sh
```

To validate a conventional subject locally:

```bash
bash scripts/check-conventional.sh "feat(discovery): add neighbor observations"
```

As the product stack is introduced, this section will gain the exact formatting, lint, test, and build commands required by the accepted architecture.

## Pull requests

Keep pull requests small enough to review confidently. A PR should:

- link the GitHub issue it advances;
- state what is in scope and intentionally out of scope;
- describe security, privacy, privilege, coverage, and migration implications where relevant;
- include tests or explain why no test applies;
- avoid unsupported product claims;
- keep generated or third-party artifacts out of the repository unless their provenance and license are understood; and
- never include real credentials, private keys, private packet captures, or unnecessary household identifying data.

Use the PR template. The title must also follow the conventional format because the project is expected to prefer squash merges.

## Review and merge expectations

Before merge:

1. inspect the final diff, not only earlier revisions;
2. make sure required CI corresponds to the exact current head commit;
3. address substantive inline review findings and add regression coverage where appropriate;
4. leave no unresolved substantive review threads; and
5. update documentation when behavior, permissions, support, or coverage boundaries changed.

Repository settings currently prefer squash merges. The squash title should therefore be a valid conventional commit subject.

## Product-specific contribution rules

Cozy SOC handles sensitive household-network data and may operate with elevated privileges. Contributions must preserve several invariants:

- discovery, observation, detection, and enforcement are distinct capabilities;
- a running process is not proof of verified coverage;
- active checks operate only on explicitly authorized scope;
- network-changing actions require deliberate consent and a recovery path;
- local-first operation must not silently depend on cloud services;
- untrusted network data such as hostnames, SSIDs, URLs, logs, and engine output is treated as hostile input; and
- specialist engines are integrated through narrow contracts rather than granting the UI arbitrary process or root access.

If a proposed change weakens one of these invariants, surface that tradeoff explicitly in the issue and PR rather than hiding it in implementation details.
