# sqlstore

`internal/sqlstore` owns Starport's relational storage contract. It is the
SQL twin of `internal/storage`, and the two packages pair the same way:

| State      | Embedded (single node)   | Connect (multi-node)   |
| ---------- | ------------------------ | ---------------------- |
| Key-value  | Badger                   | Valkey                 |
| Relational | SQLite (pure Go, no cgo) | PostgreSQL or MySQL    |

A single-node deployment gets a real database with no extra process. The
embedded SQLite file lives at `data/sqlite/starport.db` beside the Badger
directory. The development runtime keeps it in memory. When a
deployment scales past one gateway, the operator points every node at one
shared server, and nothing above this package changes.

## Selection

Configuration selects the store:

```bash
STARPORT_STORAGE_SQL_MODE=sqlite          # default; SQLITE_PATH selects the file
STARPORT_STORAGE_SQL_MODE=postgres
STARPORT_STORAGE_SQL_POSTGRES_URL=postgres://starport@db.example:5432/starport
STARPORT_STORAGE_SQL_MODE=mysql
STARPORT_STORAGE_SQL_MYSQL_DSN='starport@tcp(db.example:3306)/starport'
```

`Open` validates the configuration, dispatches on the type, and for the
network backends proves the server answers before returning. Repositories
read and write through `database/sql` on the returned `DB`. `Dialect()`
names the engine for a statement that needs backend-specific syntax.

## Schema

This package owns every migration. A migration is one `.sql` file named
`NNNN_description.sql` under `migrations/<dialect>/`. The three files with
one name represent the same logical migration. Each dialect uses its own
syntax. MySQL cannot index an unbounded `TEXT` column or parse `ON CONFLICT`. `Migrate` applies the dialect's files in
name order, once each, and records what it applied. A test holds the three
dialect sets to the same file names, and the shared contract tests hold
every backend to the same resulting behavior.

SQLite owns each migration through `BEGIN IMMEDIATE`. PostgreSQL and MySQL
hold a session lock on one connection. History checks, schema changes, and
completion records share that ownership. Startup rejects unknown migration
history.

MySQL can commit DDL before the migration completion record. The runner stores
an intent before each attempt and blocks automatic retry after interruption.
`ReconcileMySQLMigration` records an administrator's independently verified
`applied` or `reverted` outcome. It binds the decision to the migration digest,
operator identity, operation ID, and evidence digest. It never repairs schema
or repeats uncertain statements itself.

After manual repair, the operation commits its audit record, completion state,
and intent removal together. An exact operation retry returns the original
result. Reusing an operation ID with different evidence fails. The caller
authenticates the administrator and retains the evidence bytes.

## SQLite snapshots

`SnapshotSQLite` uses `VACUUM INTO` to capture committed data, including records
that remain in the WAL. It validates the database and migration history before
publishing a new private directory. The directory contains `starport.db`,
`snapshot.json`, and private publication metadata. Existing destinations remain
untouched.

`RestoreSQLiteSnapshot` checks the manifest's size and SHA256 digest before
importing the database. It rejects corrupt images, invalid foreign keys, and
unknown or incomplete migration history. It preserves older, contiguous
migration history without applying new schema files.

These methods copy SQL state only. They do not select the database for a
running gateway or approve restored permissions. The deployment coordinator
must stop cross-store writes before backup. Before admission, it must fence old
writers and reconcile independent revocation and spending evidence. The full
KV, SQL, blob, configuration, and key-access procedure remains part of CSP13.

## Tests

`go test ./internal/sqlstore/...` always proves the embedded backend. The
network backends join the same contract suite when the environment names a
server, following the Valkey precedent:

```bash
TEST_POSTGRES_URL=postgres://starport@127.0.0.1:5432/starport_test \
TEST_MYSQL_DSN='starport@tcp(127.0.0.1:3306)/starport_test' \
go test ./internal/sqlstore/...
```

Credentials use the URL or DSN fields. The examples omit them.

The suite creates a separate PostgreSQL schema or MySQL database for each
contract test. Use disposable services. PostgreSQL credentials must permit
schema creation and removal. MySQL credentials must permit database creation
and removal. Cleanup removes only the namespaces that the test created.
