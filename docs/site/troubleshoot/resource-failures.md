---
title: Resource failures
area: troubleshoot
order: 3
summary: Find and correct a quota, disk, lease, or schema failure without deleting recovery evidence.
---

A resource failure comes from a limit, a full volume, a lost lease, or a storage schema. This topic gives the symptom, the check, and the correction for each failure.

## Quota failures

| Response | Message | Cause |
| --- | --- | --- |
| `402 permission_error` | `Insufficient quota: <scope> <dimension> budget exhausted for the current <interval> window` | A gateway budget is used up. Starport also emits the `budget.exhausted` event. |
| `413` | `The stored file limit for this account is full. Delete a file to make room.` | The account or the gateway API key reached its `stored_bytes` limit. |
| `413` | `upload exceeds the configured byte bound. This deployment accepts <bytes> bytes at most` | The upload is larger than `STARPORT_FILES_MAX_UPLOAD_BYTES`. |
| `413` | A message that states the body limit | The body is larger than `STARPORT_SERVER_MAX_REQUEST_SIZE`. |

**Correction.** Raise the budget or the limit on the account or the key, or wait for the next budget window. For stored bytes, delete a file through the files API. Do not delete quota keys in the KV store to make room. Missing or invalid accounting state stops new uploads.

## Disk failures

A full volume can stop writes to the data directory, the catalog state directory, or the SQL database.

**Check.** Find each path that the gateway writes, and then measure the free space.

```bash
starport config paths --files --inspect --json
df -h <data-directory> <state-directory>
```

**Correction.** Add space to the volume, or move the directory through a storage procedure. Refer to [Move between storage modes](../storage/migration.md). The file inventory says for each entry: `This report does not authorize deletion or change retention.` Do not delete a journal, a lock, or a runtime evidence directory to make room.

## Lease failures

A deployment that shares catalog storage uses a runtime lease. Starmap holds the lease for 90 seconds and renews it every 30 seconds.

| `runtime.lease` value | Meaning |
| --- | --- |
| `lease_not_required` | The deployment shares no catalog storage. |
| `lease_held` | This instance owns the lease. |
| `lease_lost` | This instance does not hold the required lease. |

A refresh that ran under an old lease epoch ends with the reason `stale_lease_epoch`. The accepted head does not change.

**Check.** Give each process its own `STARPORT_CATALOG_STATE_DIR` on local disk. Two processes that share a state directory get the same instance identity, and the lease then fences nothing. Read `runtime.policy`. The value `policy_mismatch` blocks lease ownership while the runtime continues to serve.

**Correction.** Give each process a separate local state directory and restart it. For a policy mismatch, apply the same fleet policy to each instance. Do not clear shared storage to bypass a policy mismatch.

## Schema failures

The gateway applies pending SQL schema migrations when it opens the SQL store.

| Message | Cause |
| --- | --- |
| `relational schema is behind this binary` | The store lacks a migration of this binary. |
| `unsupported schema migration "<name>"` | The store has a migration that this binary does not know. A newer binary wrote it. |
| `503` with the refusal `schema_behind` | A configuration save found a store that is behind this binary. A save never migrates the schema. |

**Correction.** For a store that is behind, start the gateway with this binary so that it applies the migrations. For an unknown migration, run the binary that wrote the store. Do not edit the `schema_migrations` table.

## Diagnose a resource failure

### Audience

An operator who reads a quota, disk, lease, or schema error in a response or in the logs.

### Before you start

- Get the error text and the `request_id` of a failed request.
- Get an admin gateway API key in `$STARPORT_ADMIN_KEY`.

### Steps

1. Match the error text to a table in this topic.
2. Run `starport doctor --probe --json` on the gateway host.
3. For a lease failure, read `runtime.lease` and `runtime.policy` in the admin catalog status.
4. Apply the correction for that failure.
5. Send the failed request again.

### Expected result

The request succeeds, and `starport doctor --probe --json` reports `"ok": true`.

### Verification

```bash
starport doctor --probe --json | jq '.ok'
```

```text
true
```

### If it fails

Keep every file and record. Collect the doctor output, the file inventory, and the log lines with the request ID. Then follow [Recover without a console session](recovery.md).

### Related settings

- `STARPORT_FILES_MAX_UPLOAD_BYTES` sets the upload bound. The default is 512 MiB.
- `STARPORT_SERVER_MAX_REQUEST_SIZE` sets the body bound. The default is `33554432` bytes.
- `STARPORT_CATALOG_STATE_DIR` sets the catalog state directory.
