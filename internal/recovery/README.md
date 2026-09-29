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

A private on-disk index compares window totals with every retained attempt and the recorded seed consumption.
Checks include reserved capacity, consumed amounts, active disputes, and both aggregate and valuation overflow.
Correction chains must reproduce the retained attempt and remain within the original correction deadline.
Immutable state bindings establish ancestry even if authority time moves backward within the permitted interval.
Cycles and receipts outside the retained chains cause refusal.
Verification never changes balances to repair a mismatch.

Job checks bind each reservation to the original account, key, offering, operation, catalog generation, and pinned valuation.
Completed reporting requires settled accounting. Pending evidence retains its original state.
A missing job can follow interrupted creation or permitted deletion. The report names this condition without releasing the reservation.

Correction checks verify immutable intent, applied decisions, ordered report receipts, and paired budget decisions.
Superseded and pending intents remain distinct from applied corrections.
Original publication digests remain retained evidence. Inspection does not reconstruct historical transaction bytes.
Unknown correction kinds, missing chain records, conflicting decisions, and orphan records cause refusal.
Inspection never applies corrections or advances reporting.

`PrepareSQLRestore` validates the complete bundle before copying relational records into a fresh target.
It advances retained recovery epochs, closes all gates, and disables bootstrap and unused team initialization grants.
The same transaction retains an operation receipt and a persistent startup barrier.
An exact retry verifies the original source and receipt without repeating the restriction callback.
A changed operation or recovery policy causes refusal.

Ordinary application startup refuses a retained SQL import barrier before opening concept repositories.

`PrepareBundle` imports SQL, KV, blobs, and selected files under one identity bound to the complete manifest.
All target writers must remain fenced. Targets must remain separate from the backup and active deployment.
An interrupted import can resume with the same operation and manifest.
Exact retries preserve SQL restrictions, KV expirations, and blob retirement markers.
Every store retains its startup barrier.

Selected files retain their portable artifact names in an inactive staging directory.
Preparation never copies the source's absolute paths into active configuration locations.
The directory and `prepared-restore.json` receipt publish together after all component imports succeed.
An existing staging directory requires the exact receipt, file inventory, and contents.
Unexpected files, directories, and symlinks cause refusal.
Filesystem blob retries also verify every retained object and the import receipt.

A preparation receipt records completed imports. It does not prove independent history or authorize admission.
The coordinator checks SQL restrictions again before file publication.
Target writers must remain fenced through independent reconciliation, canonical file placement, and activation.
These later operations and the activation commands remain open.

These checks prove internal consistency, not completeness against later acknowledged work.
Independent later history and restore commands remain required before deployment recovery is complete.

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

## Restricted preparation

Stop and fence the source and every target writer before preparation.
Select isolated target stores through the normal Starport configuration.
Keep the deployment ID and master key consistent with the verified backup.
The preparation command does not start the gateway or fetch catalog sources.

```sh
starport backup prepare \
  --directory /private/recovery/capture-001 \
  --manifest-sha256 "$RETAINED_MANIFEST_SHA256" \
  --files-directory /private/recovery/prepared-001 \
  --operation restore-001 \
  --fencing-evidence incident-123/writer-fence \
  --json
```

The current configuration selects target KV, SQL, and blob storage.
The source configuration inside the backup does not select live destinations.
Complete source verification and deployment matching precede target creation.
The command refuses overlapping local targets and targets inside the backup.

The fencing reference enters the retained preparation receipt and component claims.
Retry with the same operation, manifest digest, fencing reference, and target stores.
A changed reference causes refusal. The reference does not stop processes or prove network isolation.

SQL setup initializes empty tables and can resume empty schema setup at a completed migration boundary.
It does not upgrade populated schemas. Unresolved MySQL migration attempts require their existing reconciliation procedure.
KV recovery access retains import barriers and does not start application maintenance.
Selected files remain inactive, including saved operator tokens and runtime identity files.

A command failure can follow completed restricted component imports.
It does not prove rollback. Preserve the targets and retry the same operation after correcting the failure.
Ordinary startup refuses imported stores while their barriers remain.

Credential-policy publication has an explicit owner procedure below.
Other file owners, independent-history reconciliation, and activation commands remain unfinished.
Do not remove barriers manually or treat a preparation receipt as permission to start inference.


## Canonical credential-policy publication

Keep every source and target writer fenced after preparation.
Use the same configured targets, deployment ID, replica ID, master key, and preparation fields.

```sh
starport backup publish-files \
  --directory /private/recovery/capture-001 \
  --manifest-sha256 "$RETAINED_MANIFEST_SHA256" \
  --files-directory /private/recovery/prepared-001 \
  --operation restore-001 \
  --fencing-evidence incident-123/writer-fence \
  --role inference-credential-policy \
  --json
```

This command verifies or resumes restricted preparation before publishing the selected role.
Current configuration selects the canonical destination. Backup paths cannot select a host destination.
The credential owner validates every retained policy record and refuses another replica identity or a pending publication journal.

Use `--role credential-policy` to publish the separate catalog-acquisition policy tree.
Starmap validates that tree through the catalog owner without resolving credentials or starting acquisition.
Both roles preserve their retained legacy default and accepted provider decisions.
Neither operation creates a missing policy default.

The complete private tree publishes without replacement. An exact retry verifies the existing tree and confirms durability.
A conflicting target remains unchanged. A failure can follow publication, so preserve the target for an exact retry.

The result lists remaining file dispositions, including administrator credentials, configuration, trust, and runtime identity.
These roles require their own recovery procedures. The command refuses unsupported roles.
The operation preserves accepted provider choices and the legacy default without resolving or using provider credentials.
KV, SQL, and blob import barriers remain closed.
File publication does not establish independent history, approve replica reuse, or permit inference.

## Target configuration and local administrator access

Keep the gateways stopped and all restore barriers closed during these steps.
Select the target configuration and environment files explicitly before preparation.
Keep current target storage endpoints, secret-manager references, and trust roots.
Use the captured configuration as evidence for comparison. Do not copy its paths or credentials over the target configuration.
Renew TLS keys and certificates through the deployment certificate procedure when necessary.

Run these commands with the target service account and its path-selection environment:

```sh
starport config validate --json
starport config paths --files --inspect --json
starport auth status --json
```

Verify that the administrator token path in both reports names the intended target file.
Resolve a mismatch before any credential write. Neither configuration validation nor filesystem inspection verifies remote trust or service reachability.
The complete preparation and owner checks remain required.

After preparation, keep the captured `local-token` and `local-token-lock` files in inactive recovery storage.
Create a fresh target administrator credential through its owner:

```sh
starport auth rotate --no-secret --json
starport auth status --json
```

The first command writes a new secret and reports its path, generation, and rotation time without printing it.
It can create a credential when the target file is absent. It does not read the captured token or open recovery barriers.
The old token and its signed console sessions do not authenticate against the new token.
A running gateway retains its old in-memory token until restart. External fencing must therefore remain in place.

Rotation is not an idempotent restore operation. Each successful call replaces the secret again.
If output fails after rotation, inspect status before choosing whether to rotate again.
Record the target token path, generation, and rotation time with the recovery incident. Do not record the secret.
An exact preparation retry preserves the fresh target token and the inactive captured files.

Repeat this procedure for each target replica with its own local token path.
This step does not recover gateway API keys, provider credentials, or SSO account grants.
Independent history reconciliation and controlled activation remain required before any gateway starts.
