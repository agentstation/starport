---
title: Back up and restore
area: storage
order: 2
summary: Capture a backup set of a stopped deployment, verify it against a retained digest, and restore it through coordinated recovery.
---

A Starport backup set is a directory that holds each storage role of one stopped deployment. Verify each set before you trust it, and keep its digest outside the set.

## Contents of a backup set

A backup set holds these artifacts. Each artifact has a SHA-256 digest in the manifest.

- A portable image of the KV store.
- An image of the relational store.
- An archive of the file bytes.
- The selected local files.

The set does not hold the master key. It holds only a reference to the key. Without the key, verification and restore cannot decrypt the stored provider credentials.

## Back up a stopped deployment

### Audience

An operator who must capture the state of a deployment before a change, or on a schedule.

### Before you start

- Make sure that the deployment uses persistent storage. `starport dev` has no backup.
- Use the same configuration as the gateway. The command reads the stores from the configuration.
- Keep the master key in a secret source. Write down its reference, such as `aws-secrets-manager:<secret-name>`.
- Create a private parent directory. The destination must be a new absolute directory under it.
- Select an operation ID and a reference to your fencing proof, such as a change ticket. These values are not secrets.

### Steps

1. Close recovery approval:

   ```bash
   starport backup close
   ```

2. Stop every writer: each gateway, each background worker, and each source acquisition process. Fence the writers so that they cannot start again. `backup close` does not stop a process.

3. Capture the stores and files:

   ```bash
   starport backup create \
     --destination /private/backups/<operation-id> \
     --operation <operation-id> \
     --fencing-evidence <change-ticket> \
     --key-reference '<master-key-reference>' \
     --json
   ```

4. Copy the manifest digest from the output to a location outside the backup set.

5. Verify the set:

   ```bash
   starport backup verify \
     --directory /private/backups/<operation-id> \
     --manifest-sha256 <retained-digest>
   ```

### Expected result

`backup close` prints `Recovery approval is closed for <deployment> at epoch <n>.` The verify command prints a line like this example:

```text
Verified <count> artifacts for <deployment> at recovery epoch <n>.
```

The output also shows counts of credential values, file records, accounts, users, teams, keys, and budget records.

### Verification

Compare the printed counts with the size of the deployment. Read each line about unconfirmed provider submissions, held reservations, and unknown budget histories. Keep unresolved work for reconciliation. Verification does not approve recovery or a retry.

### If it fails

- `backup create` refuses an open approval. Run step 1 again.
- `backup create` refuses a destination that exists. Select a new directory.
- `backup verify` refuses a missing, changed, or extra file. Do not use that set.
- `backup create` and `backup verify` need the configured master key. Supply the same key as the source deployment.

### Related settings

`STARPORT_SECURITY_MASTER_KEY`, `STARPORT_DEPLOYMENT_ID`, `STARPORT_STORAGE_MODE`, `STARPORT_STORAGE_SQL_MODE`, and `STARPORT_FILES_BACKEND`.

## After a backup

With the shared recipe, a gateway reads the approval at startup. A closed approval stops the start. To open a closed shared deployment in place, use `starport backup adopt`. Adoption needs Valkey, PostgreSQL, and object storage. Refer to [Populated adoption in place](../../RECOVERY.md#populated-adoption-in-place).

## Restore a backup set

A restore is a coordinated recovery. Keep every writer fenced until the activation is complete and a new gateway is ready. The [recovery guide](../../RECOVERY.md#activation-and-exact-retries) gives the full procedure. In summary:

1. Verify the set against the retained digest.
2. Restore into the configured target stores with `starport backup prepare`. This step does not approve admission.
3. Inspect the closed import with `starport backup inspect-import`.
4. Bind the independent history to the target digest with the same operation ID.
5. Run `starport backup activate` with a private request file. The file is 64 KiB or smaller, with mode `0600` in a `0700` directory.
6. Keep the `decision_sha256` value from the reply outside the deployment.
7. Start a new gateway with the same target configuration. Check `/health/ready` before you permit traffic or remove a fence.

The activation releases the blob store first, the KV store second, and the SQL store last. After a lost reply, run `starport backup activation-status` with the retained decision digest. Do not start a new operation. Refer to [Retained inputs](../../RECOVERY.md#retained-inputs) for the request file fields.

## Restore limits

- The tests cover a local recipe to a local recipe and a shared recipe to a shared recipe. No test covers a restore from one recipe to the other.
- A missing history cannot prove zero spend, restored permission, or a safe provider retry.
- A ready gateway does not prove caller credentials, account permission, or budget. Refer to [Completion and permission](../../RECOVERY.md#completion-and-permission).
- This release does not qualify this recovery procedure for production.

## Copy a local data directory

For the local recipe, the operator guide also permits a file copy. Stop Starport, then copy the Badger directory, the SQLite file and its `-wal` and `-shm` files, and the files directory. Copy the configuration file and keep the master key with them. Refer to [Files and paths](../configure/paths.md) for each location.
