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
| Local recipe data to the shared recipe | No tested procedure |
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

This release has no tested procedure that moves keys, credentials, usage, or identity records from Badger and SQLite into Valkey and PostgreSQL. The backup tests cover a restore between two stores of the same recipe only. Plan a new shared deployment, and create its keys and credentials again.

The `--unprefixed-valkey` flag of `starport backup create` captures a dedicated Valkey database that has no deployment prefix. Use it only with a recovery procedure that names it.

## Shared deployments of earlier releases

Do not upgrade an existing shared deployment to this release. The populated state migration for that case is not available. Refer to [Upgrade and shut down](../operate-starport/upgrades.md).
