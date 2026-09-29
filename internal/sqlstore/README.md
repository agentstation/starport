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


## Relational transfer

`SnapshotRelational` exports SQLite, PostgreSQL, or MySQL records into a portable
SQLite image. It reads one consistent SQL snapshot and preserves the audit ID
allocation counter. The transfer uses the current complete migration set.
Unknown tables, columns, triggers, and pending migration attempts stop export.
Use a dedicated PostgreSQL schema or MySQL database for this procedure.

`ImportRelational` verifies the image in private staging before writing the
target. The target must contain only the current schema and its initial
metadata. The caller must fence target writers and stop schema changes first.
Concurrent imports share migration ownership, so only one can fill the target.

The caller supplies a required recovery callback. It must close restored
permission gates in the same transaction as the imported records. A callback
failure rolls back the records. A successful import does not select the target
for a running gateway or approve recovery.

Migration 0012 retains migration repair records on every backend. It also gives
MySQL migration timestamps microsecond precision to preserve PostgreSQL values.
Finer timestamps fail transfer to network SQL. MySQL limits and collation still
apply. Incompatible keys or oversized values stop import without partial records.

Native audit counters retain their previous upper bound, including deleted IDs.
A failed import can leave an allocation gap on MySQL. Retry preserves that
counter instead of resetting it. Transfer never resets the retained allocation floor.

MySQL cannot preserve an exhausted signed audit counter.
Import refuses that target before copying records. SQLite and PostgreSQL retain
the exhausted state.

## Import activation

`ReconcileRelationalImport` applies one domain repair while the exact import barrier remains present.
It commits the repair and its evidence receipt in one transaction under native migration ownership.
The callback must use the supplied connection and keep admission closed.
An exact retry verifies the receipt without repeating the repair.
The domain owner must also check the resulting current state.
Changed evidence, another import, or completed activation causes refusal.

`ActivateRelationalImport` binds a prepared import to its snapshot, operation, restriction policy, and accepted recovery decision digest.
It runs the domain approval callback, retains completion receipts, and removes the import barrier in one transaction.
The callback must use the supplied connection for SQL writes.
It must not commit the transaction or change the schema.
The recovery coordinator must first verify independent history, external fencing, and other component releases.

A failed callback rolls back SQL approval and retains the barrier.
An exact retry verifies completion without repeating approval or changing later records.
A changed decision or import identity causes refusal.
Historical receipts survive later backups and imports.
Import clears the former native completion marker, so those receipts cannot approve the new deployment.

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
