---
title: Starmap state recovery and unrecognized journals
area: operate-starmap
order: 3
summary: Find the Starmap state that a gateway keeps, and recover safely when the runtime reports an unrecognized journal.
---

The connected Starmap runtime keeps private records on local disk. Some records are journals and locks that make an interrupted write safe to recover. This topic tells you where the records are and what to do when the runtime finds a journal that it does not recognize.

## Where the state is

`starport config paths --files --inspect --json` lists each managed file with its recovery policy. These entries hold Starmap state.

| File ID | Contents | Recovery policy |
| --- | --- | --- |
| `runtime-evidence` | Runtime identity, layer evidence, replay floors, migration records, and locks | Preserve all of it. Accepted generations live in the selected KV backend. |
| `baseline-recovery` | The `.starmap-baseline` directory with `.owner.lock` and one journal for each unfinished baseline export | Keep locks and journals until verified recovery completes. |
| `config-operation-journal` | The local configuration save journal | Keep it while a save is pending. |

Each entry in the report also says: `This report does not authorize deletion or change retention.` On a central Starmap server, run `starmap config paths --inspect --output wide` for the same facts.

## What the runtime preserves

Starmap does not delete a record that it cannot prove. A replaced lock cannot adopt earlier journals. A changed entry, an unrecorded stage, or an unsupported recovery file stays in place. A cancelled write keeps its journal for a later recovery attempt.

## Unrecognized journals

The runtime checks the materialization journals in its runtime directory before it opens retained state. An entry that is not a directory with a valid name stops the check with this error.

```text
runtime input publication conflict: materialization has an unknown journal
```

A directory with more than 4,096 journal entries stops the check with `materialization journal exceeds its entry limit`. The same check runs during a coordinated recovery inspection. A configuration operation journal with an unknown field stops a save with `decode configuration operation journal`.

An unknown journal can come from a newer binary, a partial copy, a manual edit, or a different product that shares the directory. Starmap cannot tell which. It refuses to continue so that it does not lose evidence.

## Recover from an unrecognized journal

### Audience

An operator who sees an unknown journal error at gateway start, in a refresh, or in a recovery inspection.

### Before you start

- Get shell access to the gateway host.
- Get a location for an offline backup that only operators can read.
- Find the binary version that last wrote the state directory.

### Steps

1. Do not delete, rename, or move the journal or a lock.
2. Stop each writer of the state directory, including gateways and background workers.
3. Take a consistent offline backup of the complete state directory.
4. Collect the error text, the `starport doctor --json` output, and the file inventory.
5. If a newer binary wrote the state, start that binary again.
6. Otherwise, follow the coordinated recovery in [the recovery document](../../RECOVERY.md).

```bash
starport config paths --files --inspect --json > inventory.json
starport doctor --json > doctor.json
```

### Expected result

The gateway starts with no journal error. `starport doctor --probe --json` reports `"ok": true`.

### Verification

```bash
starport doctor --probe --json | jq '.ok'
```

```text
true
```

### If it fails

Keep the writers stopped and keep the backup. Do not copy a live state directory between writers. Restore the state directory from a consistent backup through the coordinated recovery procedure. On a central Starmap server, run `starmap admin recover --id <admin-id>` only for a lost administrator credential.

### Related settings

- `STARPORT_CATALOG_STATE_DIR` sets the runtime state directory.
- Refer to [Recover without a console session](../troubleshoot/recovery.md).
