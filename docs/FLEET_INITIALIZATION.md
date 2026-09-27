# Initialize a fresh fleet

`starport fleet init` creates catalog recovery approval for a fresh Valkey and PostgreSQL deployment.
It starts no gateway, provider acquisition, or source refresh.
The command does not migrate existing data or approve a restored backend.
Complete fleet storage qualification remains part of the production catalog plan.

This build has no operator command for populated deployment adoption or recovery after a Valkey restart or failover.
CSP13 owns those commands and their qualification. Production fleet use requires that work first.

## Preconditions

Stop every gateway and schema writer that can reach these stores.
Keep them stopped until initialization finishes.
Use a dedicated empty Valkey database and an empty PostgreSQL schema.
The command permits existing migration metadata. It refuses application records and prior recovery approval.
The PostgreSQL account must read and lock all tables in the selected schema.

Select the same storage settings and deployment ID that the gateway will use:

```text
STARPORT_DEPLOYMENT_ID
STARPORT_STORAGE_MODE=valkey
STARPORT_STORAGE_VALKEY_URL
STARPORT_STORAGE_SQL_MODE=postgres
STARPORT_STORAGE_SQL_POSTGRES_URL
```

Keep credentials in the configured secret source.
The command refuses Badger, SQLite, MySQL, and Valkey Cluster for this operation.
Separate the stores used by different deployments.

## Procedure

1. Choose a stable operation ID and a non-secret audit reference.
2. Run the explicit initialization command:

```bash
starport fleet init --operation initial-deployment --evidence deployment-ticket-123 --json
```

3. Retain the result with the deployment record.
4. Create the first gateway API key in the configured storage:

```bash
starport init --configured-storage
```

5. Protect the returned key with the deployment's credential controls.
6. Start Starport with the same configuration:

```bash
starport serve
```

The initialization result identifies the deployment, recovery epoch, backend identity, and audit reference.
Approval binds to the observed Valkey process and replication history.
Normal startup reads approval and cannot create it.
A changed backend requires controlled recovery. This build cannot recover that backend.

## Failure and retry

SQL checks precede schema migration and repeat under table locks before approval.
Valkey checks its identity and empty state in one server operation.
The initializer then commits the SQL approval.
An uncertain SQL response does not prove failure: the approval might already exist.
Never delete approval or application data to force a fresh initialization.

An interrupted attempt can leave `catalog:bootstrap:v1` as the sole Valkey key.
This key contains the deployment, backend identity, operation ID, and non-secret audit reference.
Retry with the same command only after correcting the reported failure.
A matching claim permits retry while SQL remains empty and Valkey contains only that claim.
A different operation, backend, or additional record prevents retry.

A completed initialization refuses another fresh initialization, including one with the same operation ID.
Existing or uncertain state requires investigation and the migration or recovery procedure.
The command never clears existing records, reopens a closed recovery epoch, or accepts a replacement backend automatically.

## First publication

Fresh initialization grants one bootstrap permission in PostgreSQL.
Starport consumes it before the first native catalog publication in Valkey.
The final Valkey transaction still checks the original grant, native expiry, and exact predecessor.
A normal restart cannot grant another bootstrap permission.

If a crash occurs before consumption, an unused deployment can retry its first publication.
After consumption, a missing head requires controlled recovery, including when the initial publication never completed.
An uncertain SQL response stops publication. Do not clear SQL approval or catalog keys to force a retry.
A completed native publication retains its exact retry receipt even if its response was lost.
CSP13 owns the recovery procedure for an interrupted first publication and populated deployments.

The independent permission survives complete catalog KV loss while the same Valkey process remains live.
Schema migration treats existing approval rows as consumed. An empty catalog under such approval requires recovery.
These checks run during startup and catalog operations, outside inference requests.
