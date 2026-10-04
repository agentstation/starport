# Coordinated deployment recovery

`starport backup activate` completes one stopped deployment's coordinated recovery.
It replays accepted history, prepares the captured catalog, and releases native import guards in order.
It does not start a gateway or submit provider requests.
`starport backup adopt` reopens a closed populated deployment in place. See [Populated adoption in place](#populated-adoption-in-place).
Production qualification remains open under CSP13 in the canonical production catalog plan.

Keep every writer externally fenced until activation and fresh gateway readiness succeed.
This includes gateways, background workers, source acquisition, database writers, and the old primary.
An evidence reference records the operator's external proof. It does not fence a process.

## Retained inputs

Use the original verified backup, independent history package, and unchanged restore operation.
Retain each digest outside the files it identifies.
Preserve the encryption key through its configured secret reference. Do not place the key in the request file.
Target stores come from the current Starport configuration.
An imported configuration does not override current source, trust, or administrator choices.

Use a private JSON file of at most 64 KiB for the activation request.
Its parent directory and file require native owner-only access.
On POSIX systems, use directory mode `0700` and file mode `0600`.
On Windows, use a private directory with owner-only ACLs.
Unknown JSON members, invalid paths, and conflicting decision digests fail before configuration loads.

The following example shows field names and relationships. Replace every path, digest, and evidence reference before use.
Paths must be absolute and canonical on the target platform.
`Prepare` and `History` must select the same backup, scratch directory, operation, and fencing reference.
Keep the activation directory separate from backup, file staging, history, and history journal directories.
Create the private activation, scratch, and journal directories before the procedure.

```json
{
  "Prepare": {
    "Directory": "/private/recovery/original-backup",
    "ManifestSHA256": "replace-with-independent-backup-sha256",
    "ScratchDirectory": "/private/recovery/scratch",
    "Operation": {
      "ID": "restore-2026-09-30",
      "FencingEvidence": "replace-with-external-fencing-reference"
    },
    "FilesDirectory": "/private/recovery/prepared-files"
  },
  "History": {
    "Directory": "/private/recovery/original-backup",
    "ManifestSHA256": "replace-with-independent-backup-sha256",
    "ScratchDirectory": "/private/recovery/scratch",
    "Operation": {
      "ID": "restore-2026-09-30",
      "FencingEvidence": "replace-with-external-fencing-reference"
    },
    "HistoryDirectory": "/private/recovery/independent-history",
    "HistorySHA256": "replace-with-independent-history-sha256",
    "ExpectedTargetSHA256": "replace-with-closed-target-inspection-sha256",
    "JournalDirectory": "/private/recovery/history-journal",
    "ValkeyIncarnation": "replace-with-recorded-run_id:master_replid-or-empty-for-Badger",
    "Attestation": {
      "operator": "replace-with-operator-identity",
      "reference": "replace-with-independent-interval-evidence",
      "writers_fenced": true,
      "admitted_work_accounted": true,
      "complete_interval": true
    }
  },
  "ActivationDirectory": "/private/recovery/activation",
  "PreserveTargetWorkspace": true,
  "ExpectedDecisionSHA256": ""
}
```

The attestations assert external facts that Starport cannot establish.
Set them only after verifying the complete interval and previously admitted work.
Missing history cannot establish zero spending, restored permission, or safe provider retries.

## History package

`starport backup write-history` writes the independent history package for a controlled stop.
The package declares two steps: the final KV authorization and then the final SQL authorization.
The KV value comes from the backup, and the SQL value comes from the closed target.
The command records the SHA-256 digest and size of each evidence file. It does not copy the files.
It changes no target store and grants no admission.

Create an empty private history directory outside the backup, scratch, and target paths.
Keep every writer fenced while the command runs.
Supply the unchanged operation and the `target_sha256` value from `backup inspect-import`.
The command refuses a target with a different digest.

Retain the returned `history_sha256` outside the package, and use it as `HistorySHA256`.
After a failure, remove the partial history directory before you retry.

```bash
starport backup write-history \
  --directory /private/recovery/original-backup \
  --manifest-sha256 "$BACKUP_SHA256" \
  --scratch /private/recovery/scratch \
  --operation restore-2026-09-30 \
  --fencing-evidence replace-with-external-fencing-reference \
  --history-directory /private/recovery/independent-history \
  --expected-target-sha256 "$TARGET_SHA256" \
  --valkey-incarnation "$VALKEY_INCARNATION" \
  --mode planned_migration --disposition replay_complete \
  --through 2026-09-30T18:00:00Z \
  --end-reference replace-with-interval-end-reference \
  --highest-epoch 7 --epoch-reference replace-with-epoch-record-reference \
  --epoch-operator operator \
  --evidence-file source-stop=/private/recovery/evidence/source-stop.log=replace-with-evidence-reference \
  --epoch-evidence source-stop \
  --operator operator \
  --attestation-reference replace-with-independent-interval-evidence \
  --writers-fenced --admitted-work-accounted --complete-interval --json
```

The `--through` time cannot be before the backup finished.
The `--highest-epoch` value cannot be below the backup epoch.
The command writes no step for activity after the backup.
If the source admitted work after the backup, this package cannot account for it.

## Activation and exact retries

1. Verify the original backup against its independently retained manifest digest.
2. Prepare inactive target stores with `starport backup prepare`.
3. Inspect the closed import with `starport backup inspect-import`.
4. Write the history package with `starport backup write-history` and the returned target digest.
5. Run the coordinated activation with the private request file.

```bash
starport backup activate \
  --request-file /private/recovery/activation-request.json \
  --timeout 15m --json
```

The command deadline bounds this invocation. It does not define a recovery-time objective.
The workflow keeps admission closed through history replay and catalog preparation.
Native release order is blob storage, KV, then SQL.
A reply loss requires exact inspection and retry against the retained decision.
It never justifies creating a new operation or another provider submission.

Retain `decision_sha256` independently after a successful reply.
If a reply is lost, inspect the original private `ActivationDirectory/decision.json` through the controlled recovery procedure.
Verify its original source and history binding before retaining its SHA-256 digest.
Do not treat an untrusted replacement decision as operator approval.
Use that retained digest for status and sealed retries.

```bash
starport backup activation-status \
  --request-file /private/recovery/activation-request.json \
  --decision-sha256 "$RETAINED_DECISION_SHA256" --timeout 15m --json

starport backup activate \
  --request-file /private/recovery/activation-request.json \
  --decision-sha256 "$RETAINED_DECISION_SHA256" --timeout 15m --json
```

Keep the request, decision, history, and native receipts unchanged across retries.
`backup apply-history` alone does not prepare the coordinated catalog lane or activate stores.
Do not run it before this coordinated procedure.
A completed incompatible history journal requires inspection through the controlled recovery procedure.
Do not discard its evidence to force a fresh activation.

## Completion and permission

| Result field | Meaning |
| --- | --- |
| `completed_phases` | Recorded native release phases, from zero through three. |
| `historically_complete` | The retained decision completed its native release phases. |
| `current_admission_valid` | Current catalog permission and deployment approval pass inspection. |
| `restricted` | The deployment must retain its restriction. |
| `next_action` | The required operator action from current inspection. |

Historical completion does not establish current permission.
A later withdrawal can leave `historically_complete` true and `current_admission_valid` false.
Inspect current permission and deployment approval before restarting that deployment.
Status does not renew permission or change recovery state.
Errors print no completion receipt.

After valid activation, start a fresh gateway with the same current target configuration.
Check `/health/ready` before permitting traffic or removing external fences.
Gateway readiness does not prove caller credentials, account permission, or available budget.
Keep uncertain provider work and unresolved reservations for audited reconciliation.

## Local to shared recovery

A local deployment can move to Valkey, PostgreSQL, and object storage through this procedure.
Capture the fenced local deployment, then prepare and activate into empty shared stores.
The target uses the same deployment ID and encryption key.
See [Local data to the shared recipe](site/storage/migration.md#local-data-to-the-shared-recipe).

A local gateway does not read the recovery approval at startup.
A closed approval does not stop a local start.
The external fence of every local writer is mandatory, and it stays after the move.
The unchanged local stores are the rollback path.
Records that the shared deployment writes after activation do not return to the local stores.

## Populated adoption in place

Populated adoption reopens a closed deployment with its live stores in place.
It copies no data into empty targets.
Use it when the live stores hold the data that the deployment must keep, for example after a Valkey restart or a promotion.
It uses the same shared phases as activation: catalog preparation, the final authorization steps, the decision seal, ordered release, and approval.

Adoption supports a Valkey restart and a Valkey promotion.
Activation opens the current Valkey process and refuses a different process.
The approval opens only the Valkey identity that the prepared catalog topology names.

Only Valkey fleet storage with PostgreSQL and object storage supports adoption.
Badger, SQLite, and the local filesystem refuse adoption.
The shared storage configuration refuses MySQL with Valkey, so adoption on MySQL also refuses.

### Limits

- **History without prefix steps.**
  The history package must hold only the operator attestation and the two final authorization steps.
  A history package with a prefix step refuses.
  The capture of the fenced live deployment is the only base capture.

- **Acknowledged data loss.**
  Starport cannot detect an acknowledged write that Valkey lost in a restart or a promotion.
  The Valkey persistence setting controls the maximum loss.
  Adoption copies no record, so a lost write stays lost.
  The attestation must include the complete interval, which includes each lost write.

- **New catalog state directory.**
  The live catalog state directory belongs to the fenced gateway, so do not use it again.
  Adoption requires a new empty directory. The fresh gateway starts with the new directory.
  It materializes the catalog again from the adopted stores.
  If the configured directory has an owner record, preparation and the first claim refuse.
  The error names the directory, and the refusal changes nothing.

- **Restored SQL witness.**
  A restored SQL database can hold a witness record below the KV authority.
  Capture then writes its manifest and refuses the capture.
  `starport backup verify` refuses that capture.
  The refused capture files stay on disk. Delete them through the controlled recovery procedure.
  If the witness changes after capture, preparation refuses and changes nothing.

### Writer fence

Fence every writer externally before you close approval.
Keep the fence until adoption and fresh gateway readiness succeed.
This includes gateways, background workers, source acquisition, database writers, and the old primary.
After adoption, the closed approval stops dispatch through an old primary that stays reachable.
It does not stop the old primary process. The external fence is mandatory.

### Adoption inputs

Before you close approval, retain the last open witness record outside the deployment.
This record is the prior approval. Starport does not supply a command that reads it.
Close approval with `starport backup close`, and then capture the live deployment with `starport backup create`.
The capture is the backup of the adoption request.

Write the history package for the capture with `starport backup write-history` and the unchanged restore operation.
Adoption has no import inspection, so omit `--expected-target-sha256`.
Supply the current Valkey identity to `--valkey-incarnation`, and set `ValkeyIncarnation` to the same value.
After a restart or a promotion, this identity differs from the identity in the prior approval.

Configure a new empty catalog state directory in `STARPORT_CATALOG_STATE_DIR` before preparation.
The live catalog state directory holds the catalog of the prior approval, and adoption refuses it.
Use the new directory for adoption and for the fresh gateway.
Keep the old directory with the fenced gateway until you remove the fence.

Create a private preparation directory before preparation.
Keep it separate from the activation, backup, file staging, history, journal, and scratch directories.
Use the same file rules as the activation request.
Unknown JSON members, invalid paths, and conflicting digests fail before configuration loads.

```json
{
  "Activation": {
    "Prepare": { "...": "the activation Prepare fields, with Directory set to the capture" },
    "History": { "...": "the activation History fields, with the current ValkeyIncarnation" },
    "ActivationDirectory": "/private/recovery/activation",
    "PreserveTargetWorkspace": true,
    "ExpectedDecisionSHA256": ""
  },
  "PriorApproval": {
    "DeploymentID": "replace-with-deployment-id",
    "Epoch": 0,
    "Open": true,
    "BackendID": "replace-with-prior-backend-identity",
    "Evidence": "replace-with-prior-approval-evidence"
  },
  "PreparationDirectory": "/private/recovery/adoption-preparation",
  "ExpectedPreparedSHA256": ""
}
```

Replace each `...` member with the matching fields of the activation example.
Set `Epoch` to the epoch of the retained prior approval.
The attestation in `History` asserts that every writer stayed fenced and that the interval is complete.
Set it only after you verify these facts.

### Adoption procedure

1. Fence every writer externally.
2. Retain the prior approval, close approval, and capture the live deployment.
3. Write the history package for the capture with `starport backup write-history`.
4. Configure the new empty catalog state directory.
5. Prepare the adoption. Preparation places no claim.
6. Activate the adoption with the retained prepared digest.
7. Inspect the adoption with the retained prepared and decision digests.
8. Start a fresh gateway with the same configuration, and check `/health/ready`.
9. Remove the external fences only after readiness succeeds.

```bash
starport backup adopt prepare \
  --request-file /private/recovery/adoption-request.json --timeout 15m --json

starport backup adopt activate \
  --request-file /private/recovery/adoption-request.json \
  --prepared-sha256 "$RETAINED_PREPARED_SHA256" --timeout 15m --json

starport backup adopt inspect \
  --request-file /private/recovery/adoption-request.json \
  --prepared-sha256 "$RETAINED_PREPARED_SHA256" \
  --decision-sha256 "$RETAINED_DECISION_SHA256" --timeout 15m --json
```

Retain `prepared_sha256` and `decision_sha256` independently.
For an exact retry, run `activate` again with both retained digests and the unchanged request.
A retry after a lost reply places no new claim.
Inspection changes no recovery state.

Run inspection and exact retries before you start the fresh gateway.
The gateway starts its catalog. After that, inspection reports `restricted` as true and `current_admission_valid` as false.
The gateway readiness check is the current evidence after this step.

If a step fails, the deployment stays closed and restricted.
A failure before the first claim changes no native record, object, or file.
A failure after the claim keeps the claim. Do not discard its evidence.
Inspect the failure through the controlled recovery procedure, and then retry with the unchanged request.
