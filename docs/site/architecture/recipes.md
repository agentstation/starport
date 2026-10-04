---
title: Deployment recipes
area: architecture
order: 4
summary: Read the durable owners, memory state, budget admission, latency targets, checks, and recovery of each target T1 to T7.
---

A deployment recipe tells an operator how one architecture target keeps, serves, admits, and recovers its state. This topic gives one recipe for each target in the [target table](targets.md#target-table). Each recipe has six sections: durable owner per role, memory-serving state, strict admission operations, latency targets, runnable checks, and recovery.

The status line of each recipe is the same as the status in the target table. A "Supported" status means that a tested result covers the durability, the recovery, and the replica behavior of the target.

## Latency profiles

The latency values come from the engineering targets in `docs/performance-targets-v1.json`. They have the status UNVERIFIED. No measurement in this release satisfies or qualifies them. CSP22 owns their qualification on identified, dedicated runners. Each latency value is in milliseconds.

| Measure | `local` profile | `fleet` profile |
| --- | --- | --- |
| Stores | Badger, SQLite, and file system | Valkey, PostgreSQL, and object storage |
| Replicas | 1 | 3 |
| Maximum store round trip p99 | Not applicable | 0.5 |
| Paired added latency p50 | 0.5 | 1.5 |
| Paired added latency p95 | 1 | 2.5 |
| Paired added latency p99 | 2 | 4 |
| Paired added latency p99.9 | 5 | 8 |
| First-byte added latency p99 | 2 | 4 |
| First-token added latency p99 | 2 | 4 |
| Per-event forwarding p99 | 0.25 | 0.25 |

The targets apply to warm state: valid policy and credentials, pooled connections, and a disabled or missed response cache. A latency target never permits a gateway to skip required budget admission. Read [Performance](../../PERFORMANCE.md) for the workload and the evidence rules.

## T1 standalone Starmap

**Status:** Starmap product. Outside this release.

T1 is a Starmap recipe. The Starmap guides own it. Start with [Run a central Starmap server](../operate-starmap/central-server.md).

### Durable owner per role

One Starmap writer owns the generation files in a filesystem store on one host. T1 has no Starport KV, SQL, or blob role.

### Memory-serving state

Starport holds no state for T1. The Starmap guides describe the memory state of the server.

### Strict admission operations

T1 serves catalog data. It sends no inference request, so it has no budget admission.

### Latency targets

No Starport latency profile applies to T1, because T1 sends no inference request. The UNVERIFIED engineering targets in [Latency profiles](#latency-profiles) cover Starport gateways only. CSP22 qualifies those targets.

### Runnable checks

Run this command from [Connect replicas to a central server](../operate-starmap/central-server.md#verification):

```bash
curl -fsS https://<starmap-host>/api/v1/ready
```

### Recovery

Read [Starmap journals and recovery](../operate-starmap/journals-and-recovery.md). A Starport gateway that reads the server keeps its accepted catalog while the server stops.

## T2 persistent local Starport

**Status:** Supported

One gateway process keeps its state on the local host. Refer to [Run a persistent local gateway](../start/local-persistent.md).

### Durable owner per role

| Role | Durable owner |
| --- | --- |
| KV records and budget reservations | Badger at `<data>/badger` |
| Relational records | SQLite at `<data>/sqlite/starport.db` |
| File bytes | The local directory `<data>/files` |
| Accepted catalog | The catalog state directory of the instance and the baseline at `<data>/catalog/baseline` |
| Configuration | The local configuration file and the process environment |
| Master key | The secret source of the operator. No store holds it. |

### Memory-serving state

The process serves each request from this memory state:

- Immutable catalog indexes. Starport builds them before activation. One process holds at most four runtime generations.
- The applied configuration revision.
- Cached authorization. One entry lasts at most 60 seconds from verification. The cache holds at most 1,024 bundles and 16 MiB of encoded policy.
- Managed credential material. The defaults are 1,024 entries and 16 MiB of secret bytes.
- The local response cache: 1 hour and 256 MB.
- Model metadata: 6 hours and 16 MB.

The readiness probe reads memory only. Memory holds no durable record. A restart builds this state again from the durable owners, or starts with an empty cache.

### Strict admission operations

- `STARPORT_BUDGET_ADMISSION_MODE=atomic` is the default and the only supported mode.
- Each provider attempt reserves capacity in the KV store before dispatch.
- An uncertain provider charge keeps its reservation.
- A strict budget refuses unknown capacity.
- Budget windows use the time of the admission authority.
- Local quota leases, cached-balance admission, and disabled admission fail configuration validation.

In T2, Badger holds the reservation ledger.

### Latency targets

T2 uses the `local` profile in [Latency profiles](#latency-profiles). The profile holds UNVERIFIED engineering targets. CSP22 qualifies them. This release has no measured value for this recipe.

### Runnable checks

```bash
starport config paths --files
starport doctor --probe --json
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
```

### Recovery

Close approval, stop the gateway, and capture the stores with `starport backup create`. Verify the set with `starport backup verify`. Keep the master key apart from the backup set. Refer to [Back up and restore](../storage/backup-and-restore.md). A file copy of the stopped data directory is also permitted.

## T3 one production server

**Status:** Available. Single-process recovery tested. Production qualification open.

T3 uses the T2 storage recipe on durable volumes with explicit service paths. One active gateway process runs. The default Compose file is the reference container recipe for T3.

### Durable owner per role

The durable owners are the same as in [T2](#t2-persistent-local-starport). The Compose recipe puts them on three named volumes:

| Volume | Container path | Durable owner |
| --- | --- | --- |
| `starport-config` | `/var/lib/starport/config` | The configuration file |
| `starport-data` | `/var/lib/starport/data` | Badger, SQLite, file bytes, and the catalog baseline |
| `starport-state` | `/var/lib/starport/state` | The catalog runtime and the credential state |

The container has a read-only root file system. `/tmp` and the rebuildable cache at `/var/lib/starport/cache` use tmpfs. A deployment that writes outside the declared mounts stops at start.

### Memory-serving state

The memory state is the same as in [T2](#t2-persistent-local-starport). A container replacement clears it. The new container builds it again from the volumes.

### Strict admission operations

The admission operations are the same as in [T2](#t2-persistent-local-starport). Badger on the `starport-data` volume holds the reservation ledger.

### Latency targets

T3 uses the `local` profile in [Latency profiles](#latency-profiles). The profile holds UNVERIFIED engineering targets. CSP22 qualifies them. This release has no measured value for this recipe.

### Runnable checks

Run the T2 checks inside the container. The repository also has two recipe image tests. Build the image from the repository root first.

```bash
docker build -t starport-storage-recipe:local .
STARPORT_RECIPE_IMAGE=starport-storage-recipe:local go test ./internal/config -run 'TestContainerRecipePersistence|TestContainerRecipeReadOnlyMounts' -count=1
```

`TestContainerRecipePersistence` replaces the container and reads the records again. It also restores a cold backup into fresh volumes. `TestContainerRecipeReadOnlyMounts` proves the read-only root and the declared writable mounts.

### Recovery

The T2 backup procedure applies. The CSP13 tests prove a backup, a restore, and an activation from one local process to a fresh local process. The recipe image test proves a cold backup that restores into fresh volumes.

These limits apply:

- A move to the shared recipe follows [Local data to the shared recipe](../storage/migration.md#local-data-to-the-shared-recipe).
- `starport backup write-history` writes only a package for a controlled stop. No shipped command writes steps for activity after the backup.
- The [production status](../../PRODUCTION-STATUS.md) lists the open qualification work.

## T4 replicated Starport

**Status:** Fleet qualified on Valkey 7.2.14 and PostgreSQL 16.15. Failover limits apply.

Several replicas share one Valkey primary, one PostgreSQL database, and one object store. One replica at a time holds the refresh lease and owns provider acquisition. The other replicas follow the shared accepted head. Refer to [Initialize and run a fleet](../operate-starport/fleet.md).

### Durable owner per role

| Role | Durable owner |
| --- | --- |
| KV records, budget reservations, and catalog generations | One standalone Valkey 7.2.14 primary |
| Relational records and the shared configuration revision | PostgreSQL 16.15 |
| File bytes | One S3-compatible object store bucket |
| Refresh lease and accepted head | Valkey |
| Local admin token and catalog runtime | A private state directory for each replica |
| Master key | The secret source of the operator. Each replica uses the same key. |

Valkey Cluster, MySQL, and Redis are not qualified. The fleet refuses MySQL and Valkey Cluster.

### Memory-serving state

Each replica keeps the memory state of [T2](#t2-persistent-local-starport) for itself. No replica shares memory with another replica. A follower checks the shared accepted head every 30 seconds and activates a new head in memory. The local response cache stays private to each replica. A separate cache service can share response bytes. Refer to [Caches](../storage/caches.md).

### Strict admission operations

The admission operations are the same as in [T2](#t2-persistent-local-starport). Valkey holds the reservation ledger for every replica. Each reservation uses a compare-and-swap write. Two replicas therefore cannot spend the same capacity.

### Latency targets

T4 uses the `fleet` profile in [Latency profiles](#latency-profiles). The profile assumes three replicas and a store round trip p99 of 0.5 milliseconds or less. The profile holds UNVERIFIED engineering targets. CSP22 qualifies them. This release has no measured value for this recipe.

### Runnable checks

```bash
starport doctor --probe --json | jq '.ok'
curl -fsS <gateway-url>/api/v1/admin/catalog/status -H "Authorization: Bearer $STARPORT_ADMIN_KEY" | jq '.runtime.fallback'
```

The repository fleet recipe test runs two replicas from the recipe image against fresh stores. Set `STARPORT_RECIPE_IMAGE`, `TEST_VALKEY_URL`, `TEST_POSTGRES_URL`, and `TEST_BLOB_S3_ENDPOINT`, and then run:

```bash
go test ./internal/config -run TestFleetRecipeContainerRecreation -count=1
```

The test replaces both gateway containers and reads the keys, the credentials, and the files again. It starts each replica on the `embedded` and `file` sources without a GitHub route. It also runs a backup into fresh targets and inspects the restored import. Then it writes the history package and activates the restored target.

### Recovery

Close approval and fence every writer before a backup. `starport backup adopt` reopens a closed populated deployment with its live stores in place. It supports a Valkey restart and a Valkey promotion. Refer to [Back up and restore](../storage/backup-and-restore.md) and the [recovery document](../../RECOVERY.md#populated-adoption-in-place).

These limits apply:

- The first replica attachment to a Valkey primary without a replication backlog changes `master_replid`. A restart or a promotion also changes it. A bound owner then fails closed until a new admission.
- The recovery point objective is zero acknowledged writes after a persistent Valkey restart. It is also zero after an empty-target import of a fenced capture.
- A replica promotion can lose each write that only the old primary acknowledged. The product does not bound this loss.
- `starport backup write-history` writes only a package for a controlled stop. No shipped command writes steps for activity after the backup.

## T5 internal Starmap server

**Status:** Source kind available. Server is Starmap.

One internal Starmap server defines catalog membership for one or more Starport deployments. Each gateway sets `STARPORT_CATALOG_SOURCE=starmap`.

### Durable owner per role

The Starmap server owns its generations as in [T1](#t1-standalone-starmap). Each gateway owns its records with the T2, T3, or T4 recipe. Each gateway keeps its own accepted catalog in its own catalog state.

### Memory-serving state

Each gateway keeps the memory state of its own recipe. A gateway serves from its accepted catalog. It does not call the Starmap server during an inference request.

### Strict admission operations

The admission operations of the gateway recipe apply. The source API key is a catalog credential. It never pays a provider.

### Latency targets

A gateway with one process uses the `local` profile. A fleet uses the `fleet` profile. Refer to [Latency profiles](#latency-profiles). The profile holds UNVERIFIED engineering targets. CSP22 qualifies them. This release has no measured value for this recipe.

### Runnable checks

```bash
curl -fsS https://<starmap-host>/api/v1/ready
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.runtime.source_kind'
```

The second command prints `"starmap"`.

### Recovery

A gateway keeps its accepted catalog when the server stops. Recover each gateway with the procedure of its recipe. Recover the server with [Starmap journals and recovery](../operate-starmap/journals-and-recovery.md).

## T6 restricted or air-gapped installation

**Status:** File source available

No host inside the boundary reaches GitHub. An operator moves a verified catalog file across the boundary. Refer to [Imports and the air-gapped mirror](../operate-starmap/imports-and-mirrors.md).

### Durable owner per role

The process count selects the gateway recipe: T2 or T3 for one process, T4 for a fleet. The operator owns the transferred catalog file and its verification bundle. Each gateway keeps its own accepted catalog.

### Memory-serving state

Each gateway keeps the memory state of its own recipe. A late transfer changes the freshness grade only. The accepted head keeps every route.

### Strict admission operations

The admission operations of the gateway recipe apply.

### Latency targets

A gateway with one process uses the `local` profile. A fleet uses the `fleet` profile. Refer to [Latency profiles](#latency-profiles). The profile holds UNVERIFIED engineering targets. CSP22 qualifies them. This release has no measured value for this recipe.

### Runnable checks

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.runtime.source_kind, .freshness.catalog'
```

The command prints `"file"` and a freshness grade. The fleet recipe test also starts replicas on the `file` source without a GitHub route.

### Recovery

A file that fails verification leaves the accepted head in place. Move a verified file again, and then recover each gateway with the procedure of its recipe.

## T7 ephemeral development

**Status:** Supported

`starport dev` uses in-memory Badger and SQLite with isolated scratch files. Refer to [Run a temporary development gateway](../start/temporary-development.md).

### Durable owner per role

No role is durable. Badger and SQLite stay in memory. File bytes and catalog state stay in a scratch directory. The gateway loses all data at shutdown.

### Memory-serving state

The memory state is the same as in [T2](#t2-persistent-local-starport). The records also stay in memory.

### Strict admission operations

The admission operations are the same as in [T2](#t2-persistent-local-starport). The in-memory Badger store holds the reservation ledger. A shutdown removes the ledger.

### Latency targets

T7 has no latency profile. The UNVERIFIED engineering targets in [Latency profiles](#latency-profiles) do not cover development mode. CSP22 qualifies the `local` and `fleet` profiles only.

### Runnable checks

```bash
starport dev
curl --fail http://127.0.0.1:8080/health/ready
```

Persistent storage selectors fail before storage access.

### Recovery

T7 has no backup. A new start has empty stores. Refer to [Scratch directory recovery](../start/temporary-development.md#scratch-directory-recovery) for scratch files that remain after a crash.
