---
title: Storage backends
area: storage
order: 1
summary: Learn the KV, SQL, and blob roles, the backend settings for each role, and the local files that a shared deployment still needs.
---

Starport writes durable state to three roles. Each role has its own backend setting, and each backend has its own failure and backup procedure.

## The three roles

| Role | Setting | Values | Default | Holds |
| --- | --- | --- | --- | --- |
| KV | `STARPORT_STORAGE_MODE` | `badger`, `valkey` | `badger` | Keys, provider credentials, rate limits, presets, usage, file records, accepted catalog generations |
| SQL | `STARPORT_STORAGE_SQL_MODE` | `sqlite`, `postgres`, `mysql` | `sqlite` | Users, teams, memberships, and account grants |
| Blob | `STARPORT_FILES_BACKEND` | `filesystem`, `objectstore` | `filesystem` | The bytes of uploaded files |

The console storage panel shows these roles as `Records`, `Relational`, and `File bytes`.

## Why the roles stay separate

Each role has a different access pattern. The KV store holds many small records that each request reads and changes. The SQL store holds the identity plane, which needs relations and transactions. The blob store holds large file bytes. A file record goes to the KV store, and its bytes go to the blob store.

The separation also sets the recovery order. An activation releases the blob store first, the KV store second, and the SQL store last.

## The shared recipe

When `STARPORT_STORAGE_MODE=valkey`, startup applies these rules:

- The SQL mode must be `postgres`. MySQL is not permitted with Valkey.
- The files backend must be `objectstore`.
- Valkey cluster mode must stay off. Starport needs one controlled primary.

Badger permits each SQL mode and each files backend.

## Badger settings

| Setting | Default | Effect |
| --- | --- | --- |
| `STARPORT_STORAGE_BADGER_PATH` | `<data>/badger` | The database directory |
| `STARPORT_STORAGE_BADGER_SYNC_WRITES` | `true` | Writes each change to disk before the reply. `false` causes a startup warning. |
| `STARPORT_STORAGE_BADGER_COMPRESSION` | `snappy` | `none`, `snappy`, or `zstd` |
| `STARPORT_STORAGE_BADGER_GC_INTERVAL` | `5m` | The interval between value log collection attempts |
| `STARPORT_STORAGE_BADGER_GC_DISCARD_RATIO` | `0.5` | Must be more than zero and less than one |

The engine uses a 256 MiB block cache and five 64 MiB memtables. Only one process can open a Badger directory.

## Valkey settings

All names start with `STARPORT_STORAGE_VALKEY_`.

| Setting | Default | Effect |
| --- | --- | --- |
| `URL` | `valkey://localhost:6379` | `valkeys://` or `rediss://` selects TLS |
| `USERNAME`, `PASSWORD` | None | Supply the password from a secret source |
| `CA_FILE` | None | Trust roots for this connection. Without it, TLS uses the system roots. |
| `ALLOW_INSECURE` | `false` | A plaintext endpoint needs a loopback host or `true` |
| `MAX_CONNECTIONS` | `50` | The maximum number of live sockets |
| `MIN_IDLE_CONNS` | `10` | Must be less than the maximum |
| `DIAL_TIMEOUT` | `5s` | The limit for connection setup and TLS |
| `READ_TIMEOUT`, `WRITE_TIMEOUT` | `3s` | The command deadline |
| `IDLE_TIMEOUT` | `5m` | The pool cleanup interval |
| `CLUSTER_MODE` | `false` | Must be `false` for the shared recipe |

The adapter does not retry a failed command. A failed write can have an unknown result. Use the recovery procedure of the operation before you send the change again.

```dotenv
STARPORT_STORAGE_MODE=valkey
STARPORT_STORAGE_VALKEY_URL=valkeys://<valkey-host>:6379/0
STARPORT_STORAGE_VALKEY_USERNAME=<valkey-user>
STARPORT_STORAGE_VALKEY_CA_FILE=/etc/starport/certificates/valkey-ca.pem
```

## SQL settings

`STARPORT_STORAGE_SQL_SQLITE_PATH` sets the SQLite file. `STARPORT_STORAGE_SQL_POSTGRES_URL` and `STARPORT_STORAGE_SQL_MYSQL_DSN` hold service coordinates. Supply a coordinate that contains a password from a secret source. Never write it into a shared file.

## Blob settings

| Setting | Default |
| --- | --- |
| `STARPORT_FILES_PATH` | `<data>/files` |
| `STARPORT_FILES_MAX_UPLOAD_BYTES` | `536870912` |
| `STARPORT_FILES_RETENTION` | `720h` |
| `STARPORT_FILES_SWEEP_INTERVAL` | `1h` |

The object store backend uses `STARPORT_FILES_OBJECT_STORE_BUCKET`, `_REGION`, `_ENDPOINT`, `_PREFIX`, `_ACCESS_KEY_ID`, and `_SECRET_ACCESS_KEY`. It accepts an S3-compatible service, such as Amazon S3, Cloudflare R2, MinIO, or Backblaze B2. An incomplete object store setting stops startup. Refer to the [operator guide](../../OPERATOR-GUIDE.md#file-storage).

## Supported versions

The code does not state a minimum version of Valkey, PostgreSQL, MySQL, or an object store. The repository tests use Valkey and PostgreSQL container images that it pins by digest. Test your own service versions before production use.

## Local files of a shared deployment

The shared recipe moves the three roles to services. Each gateway process still uses local files:

- The configuration file, or the environment that replaces it.
- The CA files for Valkey and the other services.
- One catalog runtime directory for each instance, under `STARPORT_CATALOG_STATE_DIR`.
- The source caches under the cache root.
- The journal directories of a migration or a recovery.

Give each process its own `STARPORT_INSTANCE_ID` and runtime directory. Use the same `STARPORT_DEPLOYMENT_ID` on each replica. A different deployment ID selects different storage.

To list the local files for the selected recipe, run this command:

```bash
starport config paths --files
```

## Response cache isolation

The optional response cache needs a separate service from the durable KV store. A database number or a key prefix does not isolate memory, eviction, or failure. Refer to [Optional caches](caches.md).
