# Coordinated deployment recovery

`starport backup activate` completes one stopped deployment's coordinated recovery.
It replays accepted history, prepares the captured catalog, and releases native import guards in order.
It does not start a gateway or submit provider requests.
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

## Activation and exact retries

1. Verify the original backup against its independently retained manifest digest.
2. Prepare inactive target stores with `starport backup prepare`.
3. Inspect the closed import with `starport backup inspect-import`.
4. Bind independent history to the returned target digest and unchanged operation.
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
