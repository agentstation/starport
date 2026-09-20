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

Write MySQL migrations idempotent-safe (`IF NOT EXISTS`, `INSERT IGNORE`):
MySQL auto-commits DDL. A failed multi-statement file can leave early
statements applied with no record. The retry must tolerate them.

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
