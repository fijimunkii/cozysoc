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

## #10 implementation choice

The first v0.1 storage implementation pins:

- **`modernc.org/sqlite` v1.58.0**, a cgo-free `database/sql` driver;
- the driver's exact required **`modernc.org/libc` v1.75.6** runtime pin; and
- embedded **SQLite 3.53.4** on the macOS/Linux architectures relevant to the current Cozy SOC build path.

The cgo-free driver preserves the existing Linux CI and macOS arm64 cross-build model rather than introducing a native compiler/toolchain requirement merely to open the local database.

The first store uses a **single dedicated SQLite connection with rollback (`DELETE`) journaling**, `synchronous=FULL`, foreign keys, a bounded busy timeout, and `trusted_schema=OFF`. This is deliberately conservative: the controller is the only database owner and current v0.1 workloads do not yet justify WAL concurrency.

WAL remains available as a future measured optimization. #29 should compare it against the rollback-journal baseline under actual ingestion/query workloads before changing the production default.

Research snapshot: 2026-09-08. The selected modernc v1.58.0 tag documents SQLite 3.53.4 and the matching libc v1.75.6 dependency.

## WAL-specific constraint

SQLite's WAL documentation records a rare WAL-reset corruption bug affecting versions through 3.51.2, fixed in **3.51.3 (2026-03-13)** and later. The selected SQLite 3.53.4 runtime is newer than that fixed version.

Source: <https://www.sqlite.org/wal.html> (re-checked 2026-09-08).

Because the controller is the only database-owning process, Cozy SOC does not need multi-process direct DB concurrency as an architectural feature. Journal mode is therefore selected from measured behavior rather than assumed from common defaults.

## Alternatives considered

### PostgreSQL or another external database service

Rejected initially. It adds a second privileged/updated service, credentials, lifecycle management, and larger minimum footprint without a demonstrated need for multi-user/distributed transactional storage.

### Embedded key/value database

Viable, but the product needs relational queries across devices, temporal identity, source evidence, findings, and retention. SQLite provides those capabilities with mature migration/backup tooling and no separate service.

### Flat JSON/event files

Simple for prototypes, but weak for indexed temporal queries, migrations, transactional updates, retention, and safe concurrent controller operations.

### Direct frontend database access

Rejected. It couples UI code to physical storage, weakens authorization and migration boundaries, and makes future headless/browser clients harder to secure.

### WAL as the initial journal mode

Deferred rather than rejected. WAL can improve simultaneous read/write behavior, but Cozy SOC currently has one controller-owned connection and no measured requirement for that concurrency. Starting with rollback journaling reduces moving parts; #29 can promote WAL later with evidence.

## Consequences

- #10 defines the logical schema, migration rules, checkpoints, idempotency, and retention behavior.
- #30 defines user-facing retention/export/backup controls.
- The controller API remains the source of truth for clients.
- Hub mode can use the same storage architecture on local disk; SQLite databases must not become shared network-filesystem state between controllers.
- The driver/runtime version pair is an update unit because modernc documents its libc pin as ABI-sensitive.
- If future scale genuinely exceeds a single-controller SQLite architecture, introduce a migration ADR from measured evidence rather than pre-installing a distributed database now.

## Validation

#5/#29 should measure the core storage-growth and controller resource budgets under the reference workload, plus low-disk/restart behavior. Failing a budget triggers a schema/query/retention review before replacing the database architecture.
