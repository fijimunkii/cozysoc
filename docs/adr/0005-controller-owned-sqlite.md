# ADR 0005: Use controller-owned SQLite for the local data store

- **Status:** Accepted
- **Date:** 2026-09-08
- **Decision owners:** Cozy SOC maintainers
- **Issues:** #3, #10, #30

## Context

Cozy SOC needs durable local storage for configuration, device identity claims, normalized observations, coverage/health state, findings/incidents, and audit history.

The initial product is a single-household installation with one authoritative controller. Requiring a separate database service would materially increase installation, upgrades, authentication, backup, and failure modes without a current scale requirement.

The desktop UI, engines, and sensors do not need direct SQL access; they operate through the controller contract.

## Decision

Use **SQLite** as the initial local data store, with the database file owned and opened by the controller only.

Requirements:

- schema migrations are controller-owned and versioned;
- writes/queries are bounded and indexed for the documented workloads;
- retention and quotas are enforced by the controller;
- backups/exports use SQLite-supported consistent mechanisms rather than copying an actively changing file blindly;
- no renderer, sensor, or engine opens the database file directly; and
- the physical schema is not the public integration API.

The exact Go SQLite driver and journal mode are implementation choices that must be pinned/tested before #10 ships.

## WAL-specific constraint

SQLite's current WAL documentation records a rare WAL-reset corruption bug affecting versions through 3.51.2, fixed in **3.51.3 (2026-03-13)** and in documented backports. If Cozy SOC enables WAL mode, the selected runtime SQLite must include that fix or a documented fixed backport.

Source: <https://www.sqlite.org/wal.html> (researched 2026-09-08).

Because the controller is the only database-owning process, Cozy SOC does not need multi-process direct DB concurrency as an architectural feature. Journal mode should therefore be selected from measured behavior rather than assumed from common defaults.

## Alternatives considered

### PostgreSQL or another external database service

Rejected initially. It adds a second privileged/updated service, credentials, lifecycle management, and larger minimum footprint without a demonstrated need for multi-user/distributed transactional storage.

### Embedded key/value database

Viable, but the product needs relational queries across devices, temporal identity, source evidence, findings, and retention. SQLite provides those capabilities with mature migration/backup tooling and no separate service.

### Flat JSON/event files

Simple for prototypes, but weak for indexed temporal queries, migrations, transactional updates, retention, and safe concurrent controller operations.

### Direct frontend database access

Rejected. It couples UI code to physical storage, weakens authorization and migration boundaries, and makes future headless/browser clients harder to secure.

## Consequences

- #10 defines the logical schema, migration rules, checkpoints, idempotency, and retention behavior.
- #30 defines user-facing retention/export/backup controls.
- The controller API remains the source of truth for clients.
- Hub mode can use the same storage architecture on local disk; SQLite WAL files must never be treated as a shared network-filesystem database.
- If future scale genuinely exceeds a single-controller SQLite architecture, introduce a migration ADR from measured evidence rather than pre-installing a distributed database now.

## Validation

#5/#29 should measure the core storage-growth and controller resource budgets under the reference workload, plus low-disk/restart behavior. Failing a budget triggers a schema/query/retention review before replacing the database architecture.
