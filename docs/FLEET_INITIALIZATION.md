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
STARPORT_FILES_BACKEND=objectstore
STARPORT_FILES_OBJECT_STORE_BUCKET
STARPORT_FILES_OBJECT_STORE_REGION
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
4. Write the first shared configuration revision from the validated deployment values:

```bash
starport config init --shared --yes
```

   A shared-storage deployment uses shared configuration management. A gateway refuses to start until this revision exists.
   Run the command without `--yes` to preview the namespace and checksum.
5. Create the first gateway API key in the configured storage:

```bash
starport init --configured-storage
```

6. Protect the returned key with the deployment's credential controls.
7. Start Starport with the same configuration:

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

An interrupted attempt can leave the logical key `catalog:bootstrap:v1` as the sole Valkey key.
Its physical key includes the deployment namespace.
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

## Container recipe

`docker-compose.fleet.yml` runs Starport against external shared services.
It does not create those services. Prepare a dedicated empty Valkey database,
an empty PostgreSQL schema, and an object-store bucket first. Use TLS with
server identity verification for remote services. Use a controlled single-primary
Valkey service. Redis, MySQL, and Valkey Cluster qualification remain separate.

This is a candidate recipe for fresh initialization and qualification.
Production use requires the CSP13 recovery procedures and CSP15 failure tests.
A Valkey restart or failover changes its identity and prevents normal recovery
in this build. Do not delete state or repeat fresh initialization to bypass it.

```bash
cp .env.fleet.example .env.fleet
chmod 600 .env.fleet
# Replace every placeholder. Supply object-store credentials or an ambient role.
export COMPOSE_PROJECT_NAME=starport-node-a
docker compose --env-file .env.fleet -f docker-compose.fleet.yml build starport
docker compose --env-file .env.fleet -f docker-compose.fleet.yml run --rm starport \
  config validate --json
docker compose --env-file .env.fleet -f docker-compose.fleet.yml run --rm starport \
  fleet init --operation initial-deployment --evidence deployment-ticket-123 --json
docker compose --env-file .env.fleet -f docker-compose.fleet.yml run --rm starport \
  config init --shared --yes
docker compose --env-file .env.fleet -f docker-compose.fleet.yml run --rm starport \
  init --configured-storage --name primary-admin
docker compose --env-file .env.fleet -f docker-compose.fleet.yml run --rm starport \
  auth rotate
docker compose --env-file .env.fleet -f docker-compose.fleet.yml up -d starport
```

Retain the initialization result and protect the printed gateway key.
All replicas need the same deployment ID, master key, shared stores, catalog
policy, and configured credential sources. The example follows GitHub catalog
publications and disables provider acquisition. Select `embedded` as the catalog
source to disable GitHub pulls. An internal authority requires the separate
[authority configuration](DEPLOYMENT-TOPOLOGIES.md).

Each replica has a private named volume for its rotated admin token and local state. The shared stores retain
application records, catalog generations, recovery approval, and uploaded bytes.
Container replacement preserves its local token. A surviving process
must not depend on another replica's local files. Do not mount one writable local
state directory into several containers.

For another replica, select a different `COMPOSE_PROJECT_NAME`. Run `auth rotate`
and `up` with that project. Do not repeat fleet or gateway-key initialization.

The recipe permits one gateway per project. Its fixed container name prevents
Compose scaling from sharing the local volume. Keep each printed token private.
If its local volume is lost, rotate a new token before starting that replica.

Compose assigns an available loopback host port to each replica. Inspect the
mapping with `docker compose --env-file .env.fleet -f docker-compose.fleet.yml ps`.
Use a load balancer when testing several replicas. Shared data services need
independent backups and recovery procedures. Container volumes do not back them up.
