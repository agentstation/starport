# Deployment recovery

This package owns approval of the deployment KV incarnation and capture of portable backup bundles.
It does not replace external controls that stop gateways or isolate a former primary.

`BackupBundle` requires open KV, SQL, and blob adapters, a closed SQL recovery record, encryption-key access, and selected local files.
The caller must stop and fence writers before capture.
The coordinator compares the SQL boundary before capture and before publishing the manifest.
A changed boundary leaves partial output without a complete manifest.

The bundle contains the portable KV image, relational image, blob archive, and selected files.
Each artifact has a path, size, and SHA-256 digest.
The component receipts and the bundle manifest bind those artifacts.
The private directory retains partial output after failure for diagnosis.
The manifest is the final publication. Capture never opens the recovery gate.

`BundleManifest.Digest` returns the digest that the operator must retain independently.
`VerifyBundle` requires that digest and the encryption key.
It refuses missing, changed, additional, non-regular, and unsupported files.
Known empty publication locks are control files outside the payload inventory.
Unexpected lock content and pending publication files cause refusal.

The encrypted challenge proves access to the key selected during capture.
It does not prove that every historical credential uses that key.
`InspectBundleReferences` also decrypts every retained provider credential through its owner.
A credential encrypted with another key causes refusal.
Shared credentials remain subject to this check even when no account has a grant.

The application derives its selected-file inventory from the canonical configuration manifest.
This includes configuration, catalog runtime and baseline files, identity state, credential-selection policy, source inputs, and transport trust.

File and job owners verify record references against captured bytes.
Ready, unexpired files require their published bytes.
Pending, deleting, and expired records retain their states.
Native response receipts retain unconfirmed submissions without repeating provider requests.
Batch validation requires every recorded execution claim and its parent batch.
Missing batch files remain visible for reconciliation.

SQL identity checks verify users, teams, memberships, account grants, and templates through their owners.
The captured SQL recovery boundary must match the manifest and remain closed.
Grants can retain deleted-account references, which verification reports without restoring access.
User, team, and account checks use the 64 KiB authorization-record bound.
Template checks use the 64 MiB portable-record bound.

Gateway-key checks validate both hash-index directions, the collection count, and original storage expiry metadata.
Missing accounts and teams remain diagnostics. A deleted initial key retains its marker and cannot reopen setup.
Expired or inactive keys retain their original policy fields.

Budget checks validate retained windows, attempts, correction references, and team initialization grants.
Completed KV team initialization requires the matching consumed SQL grant, including after team deletion.
Missing current account, key, or team history remains unknown. Verification never creates zero consumption.
Uncertain attempts retain their reservations. Verification cannot settle, refund, or repeat them.

Aggregate accounting reconciliation, independent later history, and restore commands remain required before deployment recovery is complete.

A verified bundle grants no permission to resume inference.
Recovery must reconcile permission withdrawals, acknowledged spending, and uncertain work after the backup against independent evidence.
Unknown history keeps affected access restricted.
Restoring the SQL witness cannot restore permission to approve itself.

## Capture commands

The configured deployment must already contain initialized persistent stores.
Capture does not create a missing store or migrate its schema.
The configured master key must remain available through its normal configuration source.

`starport backup close` closes the SQL recovery record.
It does not stop processes or independently fence every admission path.
The operator must stop and fence all writers before capture.
Existing requests, background tasks, former primaries, and every replica belong to that procedure.

Create an unused destination below an existing private directory:

```sh
starport backup create \
  --destination /private/recovery/capture-001 \
  --operation capture-001 \
  --fencing-evidence incident-123/writer-fence \
  --key-reference vault-reference/master-key \
  --json
```

`--key-reference` records a recovery reference, never the key value.
Retain the returned manifest digest outside the backup.
The inventory records each selected path, its capture method, and its file mapping.
Loaded configuration must match the bytes read during configuration load.
Missing optional default configuration is valid.

Incomplete scans, unsupported file types, and missing loaded configuration cause refusal.
Native adapters capture database and blob contents instead of copying engine files as ordinary configuration files.

Badger opens read-only on Linux and macOS.
Windows requires a native exclusive open, which can recover engine state.
The Windows capture connection does not run application maintenance tasks.
All platforms require stopped writers.
Valkey capture binds the observed backend incarnation and refuses a pending import barrier.

Verify the bundle without opening live stores:

```sh
starport backup verify \
  --directory /private/recovery/capture-001 \
  --manifest-sha256 "$RETAINED_MANIFEST_SHA256"
```

Verification checks captured bytes, retained credentials, files, jobs, batches, and execution claims.
It uses private snapshot copies and streams large payloads.
The `--scratch` option selects an existing private directory for these copies.
The default is the backup directory's parent.
The command reports unfinished work, missing owner references, held reservations, and unknown budget histories.

A recorded result digest with pending output remains a valid interrupted state.
A missing execution claim causes refusal because it cannot prove that the line never started.
It does not reopen admission or establish independent recovery history.
External environment settings, credential sources, cloud permissions, and upstream authority trust remain explicit recovery requirements.
