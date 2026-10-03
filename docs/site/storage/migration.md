---
title: Move between storage modes
area: storage
order: 3
summary: Learn which storage moves this release supports, move catalog runtime state with a phased command, and avoid a setting change that strands data.
---

A storage setting selects a store. It does not copy data into that store. Use only the moves that this topic names, and make a verified backup before each move.

## What a setting change does

| Change | Result |
| --- | --- |
| `STARPORT_STORAGE_MODE` from `badger` to `valkey` | The gateway opens an empty Valkey store. The Badger data stays on disk. |
| `STARPORT_STORAGE_SQL_MODE` or a SQL path | The gateway opens a different relational store. |
| `STARPORT_FILES_BACKEND` or `STARPORT_FILES_PATH` | The gateway reads file bytes from a different location. |
| `STARPORT_DEPLOYMENT_ID` | The gateway selects different records in the same services. It does not rename or copy records. |
| A root or path setting | The gateway uses the new path. It does not copy files. |

Each row strands the previous data. The new store does not contain the old keys, credentials, or records.

## Moves in this release

| Move | Support |
| --- | --- |
| Catalog runtime directory to a new path | `starport migrate runtime` |
| Data of an earlier release at its old default path | Keep the old path with explicit path settings |
| Configuration authority between local and shared | `starport config migrate` |
| A new, empty shared deployment | `starport fleet init` |
| A backup set to stores of the same recipe | Coordinated recovery |
| Local recipe data to the shared recipe | Coordinated recovery into empty shared stores. Refer to [Local data to the shared recipe](#local-data-to-the-shared-recipe). |
| An existing shared deployment to this release | Not supported. Do not upgrade it. |

## Move the catalog runtime directory

**Audience:** an operator who must put the catalog runtime of one instance on a different volume or path.

**Before you start:**

- Stop the gateway of this instance.
- Read the scheduler identity from the status of the original runtime.
- Create a private journal directory.
- Keep the same catalog KV backend, deployment ID, instance ID, and scheduler identity during the move.

**Steps:**

1. Record the source inventory and bind the journal to the catalog KV store:

   ```bash
   starport migrate runtime prepare \
     --operation <operation-id> \
     --source /var/lib/starport/state/catalog/runtime/<instance> \
     --target /mnt/<volume>/starport/runtime/<instance> \
     --journal /var/lib/starport/migration-journal \
     --identity <scheduler-identity> \
     --json
   ```

2. Run `stage` with the same five values. It copies and verifies private staging files.
3. Run `publish` with the same five values. It installs the target and retires the source runtime.
4. In the primary configuration file, set `STARPORT_CATALOG_STATE_DIR` to the target path and `STARPORT_SCHEDULER_IDENTITY` to the retained identity.
5. In the same file, save the unchanged KV selection: `STARPORT_STORAGE_MODE` and the absolute `STARPORT_STORAGE_BADGER_PATH` or `STARPORT_STORAGE_VALKEY_URL`. Keep credentials in their secret source.
6. Run `complete` with the same five values.
7. Start the gateway.

**Expected result:** Each phase exits with status 0. The `--json` result names `journal_directory` and `host_journal_directory`.

**Verification:** `starport config paths` shows the target path as the catalog runtime location. `/health/ready` returns `200` after the start.

**If it fails:** Correct the reported cause. Then run the same phase again with the same five values. Do not delete the journal or the source files before `complete`. A different catalog store cannot resume the operation.

**Related settings:** `STARPORT_CATALOG_STATE_DIR`, `STARPORT_SCHEDULER_IDENTITY`, `STARPORT_INSTANCE_ID`.

This command does not move Badger, SQLite, a SQL service, uploaded files, or the catalog workspace. An environment value alone cannot complete the move, because `complete` reads the configuration file.

## Paths of an earlier release

Before an upgrade, run `starport config paths --legacy --json`. The report lists old paths that the new defaults leave behind. Startup refuses such a conflict and keeps the old files. Set explicit paths to keep the old locations. Refer to [Files and paths](../configure/paths.md#locations-of-earlier-releases).

## Configuration authority

`starport config migrate --to shared` and `starport config migrate --to local --yes` change the authority for catalog settings. They write a configuration revision with an audit record. They do not move storage data. Refer to [Configuration sources and precedence](../configure/precedence.md#shared-management).

## A new shared deployment

`starport fleet init` approves empty Valkey and PostgreSQL stores. Stop every gateway before you run it. It refuses application records and an earlier approval. It does not move existing data. Refer to [Initialize a fresh fleet](../../FLEET_INITIALIZATION.md#procedure) and to [Initialize and run a fleet](../operate-starport/fleet.md).

## Local data to the shared recipe

**Audience:** an operator who must move one local deployment from Badger, SQLite, and a file directory to Valkey, PostgreSQL, and object storage.

The move is a coordinated recovery into empty shared stores. It copies gateway keys, accounts, budgets, provider credentials, uploaded files, users, teams, memberships, grants, usage, and audit records. It does not change the local stores. The local stores are the rollback path.

The move needs a full stop. The local deployment cannot serve requests from the start of the capture until the first shared gateway is ready.

### External fence

`starport backup close` does not stop a local gateway. Only a shared gateway reads the approval at startup. A local gateway can start again after the capture.

The external fence is mandatory. Stop each local writer, and prevent its restart outside Starport. Local writers include the gateway, each background worker, and source acquisition. Keep the local fence after the move. A local gateway that starts again accepts the same keys and spends the same budgets in different stores.

### Before you start

- Provision Valkey, PostgreSQL, and an object store bucket for the shared recipe. Each store must be empty. The [fleet requirements](../operate-starport/fleet.md#fleet-requirements) apply.
- Do not run `starport fleet init` on these stores. The activation approves them.
- Write the target configuration. Use the same `STARPORT_DEPLOYMENT_ID` and the same master key as the local deployment. The `deployment_id` value of the `backup create` output gives the local deployment ID.
- Give each shared gateway a new, empty catalog state directory.
- Create the private directories that the [recovery guide](../../RECOVERY.md#retained-inputs) names: a parent for the backup set, a scratch directory, and the activation, history, and journal directories.
- Select an operation ID and a fencing reference. These values are not secrets.

### Steps

1. Stop and fence every local writer. Refer to [External fence](#external-fence).
2. With the local configuration, close recovery approval:

   ```bash
   starport backup close
   ```

3. With the local configuration, capture the local stores:

   ```bash
   starport backup create \
     --destination /private/backups/<capture-id> \
     --operation <capture-id> \
     --fencing-evidence <change-ticket> \
     --key-reference '<master-key-reference>' \
     --json
   ```

   Keep `manifest_sha256` and `deployment_id` outside the backup set.

4. Verify the set with `starport backup verify`. Refer to [Back up a stopped deployment](backup-and-restore.md#back-up-a-stopped-deployment).
5. With the target configuration, restore into the empty shared stores:

   ```bash
   starport backup prepare \
     --directory /private/backups/<capture-id> \
     --manifest-sha256 <retained-digest> \
     --scratch /private/recovery/scratch \
     --files-directory /private/recovery/prepared-files \
     --operation <restore-operation-id> \
     --fencing-evidence <change-ticket> \
     --json
   ```

   Keep the `prepared.boundary` values. The import barriers stay. A shared gateway refuses to start before activation.

6. Inspect the closed import with `starport backup inspect-import`. Supply the same backup flags and restore operation. Supply the prepared boundary with the `--expected-deployment`, `--expected-recovery-epoch`, `--expected-recovery-evidence`, and `--expected-recovery-backend` flags. Supply `0` to `--kv-replay-sequence`, `--sql-replay-sequence`, and `--blob-replay-sequence`. Supply `run_id:master_replid` of the Valkey server to `--valkey-incarnation`. Keep `target_sha256`.
7. Bind the independent history to `target_sha256` and the same restore operation. Then run `starport backup activate` with the private request file. Refer to [Activation and exact retries](../../RECOVERY.md#activation-and-exact-retries). Keep `decision_sha256` outside the deployment.
8. Start the first shared gateway with the target configuration. Check `/health/ready` before you permit traffic.

### Expected result

`starport backup activate` reports `completed_phases` 3, `historically_complete` true, `current_admission_valid` true, and `restricted` false. The shared gateway serves the catalog generation of the local deployment.

### Verify parity

1. Compare the `references` counts of `backup create`, `backup prepare`, and `backup inspect-import`. Each count must be equal. The counts include keys, accounts, credential records, file records, users, teams, memberships, grants, budget windows, and budget records.
2. The reference report does not count usage or audit records. Compare the usage and audit listings of the shared gateway with the listings of the local gateway before the stop.
3. Send one request with an existing gateway key. The account and budget of the key must be the same as before the move.
4. Read one uploaded file. Its bytes must be the same as before the move.

### Join a second replica

Use the same target configuration with a different `STARPORT_INSTANCE_ID` and a new, empty catalog state directory. Do not run `backup prepare`, `backup activate`, or `fleet init` again. The replica loads the catalog generation that the activation accepted and accepts the same keys. Run `starport auth rotate` on the replica to make its local admin token. Refer to [Related settings](../operate-starport/fleet.md#related-settings).

### Roll back

You can roll back while the local stores are unchanged.

1. Stop and fence every shared gateway.
2. Remove the local fence, and start the local gateway with the local configuration.
3. Check `/health/ready`.

The local gateway serves the records of the capture. Records that the shared deployment wrote after activation stay in the shared stores. The local deployment does not receive them. Reconcile that usage and spend separately.

`backup prepare` refuses shared stores that hold records. Use new, empty stores for a new attempt.

### If it fails

- `backup prepare` refuses a KV store, a SQL store, or an object store that holds records. The refusal does not change those records. Use empty stores.
- `backup prepare` refuses a target with a different deployment ID or a different master key.
- `backup inspect-import` and `backup activate` refuse a SQL approval record that changed after preparation.
- `backup create` refuses an open approval. Run `starport backup close` again.
- After a failure, keep every fence. The local stores stay the rollback path.

### Limits

- The tests use a populated fixture: two accounts, three gateway keys, two provider credentials, two files, two users, one team, two grants, four usage records, and three audit records. The team in the fixture has no budget. Team budget history is not in the test.
- The container recipe test runs capture, preparation, inspection, and the refusals with the image. It stops before activation, because activation needs the independent history package. The in-process tests cover activation, a second replica, and rollback.
- The tests use plaintext Valkey on a private network. A production fleet uses TLS.
- After the move, the fleet head has a recovery origin and no lease. The first gateway that gets the lease publishes the same generation again at the next revision. A promotion after the move completes only while a running gateway holds the fleet lease. A test promotes a newer packaged baseline through a running shared gateway after the move, with a request that names the moved revision. Refer to [Baseline promotion](../../OPERATOR-GUIDE.md#baseline-promotion).
- The move copies the deployment keys without a filter. The tests find no `provider-health:instance:`, `provider-latency:instance:`, or `catalog_migration:v1:` keys on the target, because a local gateway does not write them. If you ran `starport migrate runtime prepare` on the local deployment, its receipt moves too. On the shared target, the same operation ID then refuses because the store identity is different. Use a new operation ID.
- This release does not qualify this procedure for production.

The `--unprefixed-valkey` flag of `starport backup create` captures a dedicated Valkey database that has no deployment prefix. Use it only with a recovery procedure that names it.

## Shared deployments of earlier releases

Do not upgrade an existing shared deployment to this release. The populated state migration for that case is not available. Refer to [Upgrade and shut down](../operate-starport/upgrades.md).
