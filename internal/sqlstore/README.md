# sqlstore

`internal/sqlstore` owns Starport's relational storage contract. It is the
SQL twin of `internal/storage`, and the two packages pair the same way:

| State      | Embedded (single node)   | Connect (multi-node)   |
| ---------- | ------------------------ | ---------------------- |
| Key-value  | Badger                   | Valkey                 |
| Relational | SQLite (pure Go, no cgo) | PostgreSQL or MySQL    |

A single-node deployment gets a real database with no extra process: the
embedded SQLite file lives at `data/sqlite/starport.db` beside the Badger
directory, and the development runtime keeps it in memory. When a
deployment scales past one gateway, the operator points every node at one
shared server, and nothing above this package changes.

## Selection

The store is selected by configuration alone:

```bash
STARPORT_STORAGE_SQL_MODE=sqlite          # default; SQLITE_PATH selects the file
STARPORT_STORAGE_SQL_MODE=postgres
STARPORT_STORAGE_SQL_POSTGRES_URL=postgres://starport@db.example:5432/starport
STARPORT_STORAGE_SQL_MODE=mysql
STARPORT_STORAGE_SQL_MYSQL_DSN='starport@tcp(db.example:3306)/starport'
```

`Open` validates the configuration, dispatches on the type, and for the
network backends proves the server answers before returning. Repositories
read and write through `database/sql` on the returned `DB`; `Dialect()`
names the engine for the rare statement that cannot be written once for
all three.

## Schema

This package owns every migration. A migration is one `.sql` file named
`NNNN_description.sql` under `migrations/<dialect>/`; the three files with
one name are the same logical migration, written per dialect because the
engines disagree on the margins (MySQL cannot index an unbounded `TEXT`
column or parse `ON CONFLICT`). `Migrate` applies the dialect's files in
name order, once each, and records what it applied. A test holds the three
dialect sets to the same file names, and the shared contract tests hold
every backend to the same resulting behavior.

Write MySQL migrations idempotent-safe (`IF NOT EXISTS`, `INSERT IGNORE`):
MySQL auto-commits DDL, so a failed multi-statement file can leave early
statements applied with no record, and the retry must tolerate them.

## Tests

`go test ./internal/sqlstore/...` always proves the embedded backend. The
network backends join the same contract suite when the environment names a
server, following the Valkey precedent:

```bash
TEST_POSTGRES_URL=postgres://starport@127.0.0.1:5432/starport_test \
TEST_MYSQL_DSN='starport@tcp(127.0.0.1:3306)/starport_test' \
go test ./internal/sqlstore/...
```

Credentials ride the URL or DSN in their usual place; the examples omit
them so no scanner mistakes documentation for a secret.

The suite drops and recreates its tables, so point it at a disposable
database.
