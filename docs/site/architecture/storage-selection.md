---
title: Select storage for a target
area: architecture
order: 3
summary: Match each architecture target to a storage recipe, and learn the limits and the triggers that require a move.
---

Starport stores three kinds of data in three separate roles. The process count of the target decides the backend for each role.

## Storage roles

| Role | Holds | Local backend | Shared backend |
| --- | --- | --- | --- |
| KV records | Keys, provider credentials, rate limits, presets, usage, file records, catalog generations | Badger | Valkey |
| Relational records | Users, teams, memberships, and account grants | SQLite | PostgreSQL |
| File bytes | Uploaded files | Local directory | S3-compatible object store |

A cache is not a storage role. Refer to [Optional caches](../storage/caches.md).

## Recipe for each target

| Target | KV | Relational | File bytes |
| --- | --- | --- | --- |
| T7 development | In-memory Badger | In-memory SQLite | Scratch directory |
| T2 persistent local | Badger | SQLite | Local directory |
| T3 one production server | Badger on a durable volume | SQLite on a durable volume | Local directory on a durable volume |
| T4 replicated | Valkey | PostgreSQL | Object store |

T5 and T6 use the recipe of T2, T3, or T4. Select it from the gateway process count.

## Shared storage rules

Starport checks the shared recipe at startup. When `STARPORT_STORAGE_MODE=valkey`, these rules apply:

- `STARPORT_STORAGE_SQL_MODE` must be `postgres`.
- `STARPORT_FILES_BACKEND` must be `objectstore`.
- `STARPORT_STORAGE_VALKEY_CLUSTER_MODE` must be `false`.

A configuration that breaks a rule stops startup with a fixed message. For example: "shared storage requires PostgreSQL for relational state and recovery approval".

Set `STARPORT_DEPLOYMENT_ID` to the same value on every replica of one deployment. A changed value selects different storage. It does not move the existing records.

## Availability limits

- One Badger directory serves one process. Do not share it between processes.
- One SQLite file serves one host. It does not synchronize between replicas.
- A local file directory serves one node. Another replica cannot read its files.
- The shared recipe has no qualified failover result in this release. The [production status](../../PRODUCTION-STATUS.md) lists the open work.
- Valkey with PostgreSQL is the primary replicated recipe that the production work will qualify. Redis and MySQL need their own compatibility evidence.

## Durable write settings

Badger writes with `STARPORT_STORAGE_BADGER_SYNC_WRITES=true` by default for persistent configuration. A value of `false` causes a startup warning. Acknowledged writes can then disappear after a host failure.

## Triggers to move to shared storage

Move from a local recipe to the shared recipe when one of these conditions occurs:

- You need a second gateway process for capacity or for failover.
- Two hosts must serve the same keys, budgets, or uploaded files.
- You need a rolling upgrade without a full stop.

Move from T7 to T2 when you want to keep keys or records after a restart.

## Before you move

A change of `STARPORT_STORAGE_MODE` alone opens different storage. It does not copy data. Plan the move with [Move between storage modes](../storage/migration.md). Back up first with [Back up and restore](../storage/backup-and-restore.md#before-you-start).

The operator guide says: "Do not upgrade an existing shared deployment to this candidate." Read [Storage modes](../../OPERATOR-GUIDE.md#storage-modes) before a shared deployment.
