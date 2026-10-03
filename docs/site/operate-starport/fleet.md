---
title: Initialize and run a fleet
area: operate-starport
order: 3
summary: Approve fresh shared stores for a fleet, start the first replica, add replicas, and understand catalog retention in shared storage.
---

A fleet is two or more gateways that share one deployment. The replicas share Valkey, PostgreSQL, and object storage. Each replica keeps its own local state directory and local admin token.

## Fleet requirements

| Requirement | Value |
| --- | --- |
| Key-value store | Standalone Valkey with one primary. Valkey Cluster is refused. |
| SQL store | PostgreSQL. Badger, SQLite, and MySQL are refused for a fleet. |
| File bytes | Object storage. Shared storage refuses local file bytes. |
| Deployment | The same `STARPORT_DEPLOYMENT_ID` on each replica |
| Secrets | The same master key and credential sources on each replica |
| Local state | One private directory for each replica, never shared |

`starport fleet init` approves fresh stores only. It refuses stores that hold application records or an earlier approval. It does not migrate data, approve a restored backend, or start a gateway.

## Populated deployments

For a deployment that already holds data, use the `starport backup` commands. `starport backup adopt` reopens a closed populated deployment with its live stores in place. It supports a Valkey restart and a Valkey promotion. Refer to [Back up and restore](../storage/backup-and-restore.md) and [the recovery document](../../RECOVERY.md#populated-adoption-in-place).

Never delete approval records or catalog keys to force a fresh initialization. The command never clears records and never accepts a replacement backend automatically.

## Catalog retention in shared storage

Starport keeps catalog generations in Valkey for the fleet. Collection never removes protected content. Protected content includes the current publication, the accepted publication, accepted rollback history, and active readers. Accepted history keeps up to 32 generation IDs.

| Limit | Value |
| --- | --- |
| Retained publications | 96 |
| Reader claims | 256 |
| Encoded retained and staged bytes | 2 GiB |

At a limit, the store refuses another upload. Run explicit collection or release protected generations, and then try again. No catalog chunk expires by time. Maintenance does not run on inference requests.

## Initialize a fresh fleet

### Audience

An operator who starts a new fleet on empty shared stores.

### Before you start

- Stop each gateway and each schema writer that can reach the stores.
- Prepare an empty Valkey database, an empty PostgreSQL schema, and an object storage bucket.
- Give the PostgreSQL account permission to read and lock each table in the schema.
- Put each credential in the configured secret source.
- Choose a stable operation ID and an audit reference that holds no secret.

### Steps

1. Set the same storage settings and deployment ID that the gateways will use.
2. Approve the fresh stores.
3. Write the first shared configuration revision.
4. Create the first gateway API key in configured storage.
5. Keep the printed key in your credential store.
6. Start the first replica.

```dotenv
STARPORT_DEPLOYMENT_ID=<deployment-id>
STARPORT_STORAGE_MODE=valkey
STARPORT_STORAGE_SQL_MODE=postgres
STARPORT_FILES_BACKEND=objectstore
```

```bash
starport fleet init --operation initial-deployment --evidence <audit-reference> --json
starport config init --shared --yes
starport init --configured-storage
starport serve
```

Run `starport config init --shared` without `--yes` to preview the namespace and checksum first.

### Expected result

The `fleet init` result names the deployment, the recovery epoch, the backend identity, and the audit reference. The gateway starts and reads the shared configuration revision.

### Verification

```bash
starport doctor --probe --json | jq '.ok'
curl -fsS <gateway-url>/api/v1/admin/catalog/status -H "Authorization: Bearer $STARPORT_ADMIN_KEY" | jq '.runtime.fallback'
```

### If it fails

Correct the reported failure, and then run the same command with the same operation ID. A retry works only while SQL is empty and Valkey holds only the claim of that operation. A completed initialization refuses a second fresh initialization. An uncertain SQL response does not prove a failure, because the approval can exist.

### Related settings

- To add a replica, use the same configuration and a new local state directory. Do not run `fleet init` or `init --configured-storage` again.
- Run `starport auth rotate` on each new replica to make its local admin token.
- Refer to [Storage backends](../storage/backends.md).
