---
title: Architecture targets
area: architecture
order: 1
summary: Compare the seven architecture targets T1 to T7 and see which targets this release supports.
---

An architecture target names the processes, the catalog authority, and the storage recipe of one deployment. This topic lists the seven targets and the status of each target in this release.

## Target table

| ID | Architecture | Storage recipe | Status in this release |
| --- | --- | --- | --- |
| T1 | Standalone Starmap CLI or server | Starmap generation files on one host | Starmap product. Outside this release. |
| T2 | Persistent local Starport | Badger, SQLite, and local file bytes | Supported |
| T3 | Starport on one production server | Badger and SQLite on durable volumes | Available. Single-process recovery tested. Production qualification open. |
| T4 | Replicated Starport with direct catalog updates | Valkey, PostgreSQL, and object storage | Fleet qualified on Valkey 7.2.14 and PostgreSQL 16.15. Failover limits apply. |
| T5 | Internal Starmap with Starport deployments | Starmap uses T1. Each gateway uses T2, T3, or T4. | Source kind available. Server is Starmap. |
| T6 | Restricted or air-gapped installation | T1 to T5 storage, by process count | File source available |
| T7 | Ephemeral development composition | In-memory Badger and SQLite, scratch files | Supported |

"Qualification" means a tested result for durability, recovery, and replica behavior. An adapter that starts does not prove a qualified recipe. [Deployment recipes](recipes.md) gives the durable owners, the checks, and the recovery of each target.

## T1 standalone Starmap

One Starmap writer follows GitHub or a selected local input. It stores generations in a filesystem store and needs no SQL database. Starport does not ship this target. Refer to [Run a central Starmap server](../operate-starmap/central-server.md).

## T2 persistent local Starport

One gateway process uses Badger for records, SQLite for relational data, and a local directory for file bytes. It reads the embedded baseline and the public source. It fits a developer or a small team. Refer to [Run a persistent local gateway](../start/local-persistent.md).

## T3 one production server

T3 uses the T2 storage recipe on durable volumes with explicit service paths. It runs one active gateway. Tests prove a backup and a restore from one local process to a fresh local process. The recipe image test also restores a cold backup into fresh volumes. The [production status](../../PRODUCTION-STATUS.md) states the open qualification work. Back up each store before you depend on this target.

## T4 replicated Starport

Several gateway replicas share one Valkey service, one PostgreSQL database, and one object store. One replica at a time owns provider acquisition. The other replicas follow the shared accepted head.

```text
              +-------------+
 clients ---> | balancer    |
              +------+------+
                     |
        +------------+------------+
        |            |            |
   +----v----+  +----v----+  +----v----+
   |Starport1|  |Starport2|  |StarportN|
   +----+----+  +----+----+  +----+----+
        |            |            |
   +----v------------v------------v----+
   | Valkey | PostgreSQL | object store |
   +-----------------------------------+
```

Each replica keeps its own catalog state directory. The fleet tests qualify Valkey 7.2.14 with PostgreSQL 16.15.

The first replica attachment to a Valkey primary without a replication backlog changes `master_replid`. A restart or a promotion also changes it. A bound owner then fails closed until a new admission. A promotion can lose a write that only the old primary acknowledged. The [T4 recipe](recipes.md#t4-replicated-starport) states each limit.

The operator guide says: "Do not upgrade an existing shared deployment to this candidate." Refer to [Initialize and run a fleet](../operate-starport/fleet.md).

## T5 internal Starmap server

One internal Starmap server defines catalog membership for one or more Starport deployments. Each gateway sets `STARPORT_CATALOG_SOURCE=starmap` and reads the server.

```text
 GitHub catalog/v1 ---> +----------------+
                        | Starmap server |
                        +-------+--------+
                                | server-sent events
              +-----------------+-----------------+
              |                 |                 |
        +-----v-----+     +-----v-----+     +-----v-----+
        | Starport  |     | Starport  |     | Starport  |
        | (T2/T3)   |     | (T4 fleet)|     | (T4 fleet)|
        +-----------+     +-----------+     +-----------+
```

A gateway keeps its accepted catalog when the server stops. Refer to [Catalog source topologies](topologies.md).

## T6 restricted or air-gapped installation

No host inside the boundary reaches GitHub. An operator moves a verified catalog file across the boundary. Each gateway sets `STARPORT_CATALOG_SOURCE=file` and `STARPORT_CATALOG_ACQUISITION_ENABLED=false`.

```text
 outside            | boundary
 GitHub ---> puller | ---> catalog file ---> Starport 1 ... Starport N
                    |      (manual transfer)
```

Refer to [Imports and the air-gapped mirror](../operate-starmap/imports-and-mirrors.md).

## T7 ephemeral development

`starport dev` uses in-memory Badger and SQLite with isolated scratch files. It loses all data at shutdown. Persistent storage selectors fail before storage access. Refer to [Run a temporary development gateway](../start/temporary-development.md).

## Specification targets

The seven targets come from the engineering specification for the catalog lifecycle. Some targets describe production goals. Treat only a target that the [target table](#target-table) marks as supported as a complete recipe. [Deployment recipes](recipes.md) gives the checks and the limits of each target. For the other targets, read [Select storage](storage-selection.md) and the production status before you deploy.
