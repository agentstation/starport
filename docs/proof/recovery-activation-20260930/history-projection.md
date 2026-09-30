# Populated recovery contract

This design covers controlled recovery of an existing populated deployment after a native KV restart or promotion.
The offline projection has source and tests. Complete populated adoption has no implementation or qualification.
The current empty-target import contract remains unchanged.

## Offline expected state

`recovery.ProjectIndependentHistory` accepts an original verified `RestoreSource`, the original independent history package, and a private local directory.
The package binds the backup, deployment, operation, selected target identity, original interval cutoff, and explicit completeness statements.
Those statements do not prove external writer fencing or interval completeness.

The operation copies original KV, SQL, and blob images into private storage.
Existing domain decoders apply the non-final history to those copies.
Original expiry deadlines and the original interval cutoff remain unchanged.
The projection does not rotate final authority.
It opens no configured target or network service.

Ordinary account, key, and grant writers advance authorization revision stamps.
Domain replay leaves the original stamps in the projected copies.
The original final history preimages separately bind the captured KV revision SHA and complete SQL stamp.
Revision owners verify those preimages before domain comparison.
Absent, changed, stale, or substituted revision evidence cannot qualify the captured state.

`IndependentHistoryProjection.CompareCaptured` checks a separately verified immutable capture against that expected state.
All domain KV bytes and expirations must match, apart from the separately checked revision singleton.
SQL domain rows and the audit allocation watermark must match.
The SQL census separately fingerprints witness, native controls, and authorization revision rows.
Blob bytes and retirements must match through the actual portable archive owner.
The projection checks the root manifest receipt and component images again at use.

The result always remains restricted.
Native claims, independently known SQL approval, catalog identity, selected files, and external fencing remain required.
No diagnostic digest report can substitute for those private owner capabilities.
Unrecorded catalog publication changes refuse exact comparison.
The current typed history format does not supply post-backup catalog publication transitions.

## Exact retry and private workspace

The private binding precedes all scratch writes.
It binds the original backup, history digest, operation, cutoff, final revision preimages, and completeness statements.
A completed retry reopens original retained outputs and checks their exact receipts.
It does not capture a changed live target or renew any permission deadline.
An incomplete workspace remains restricted and preserved for inspection.
Changed bindings, outputs, or unexpected root files refuse.

The projection removes only exact-identity private mutable copies after completion.

Every attempt also checks the complete original backup through `VerifyBundle`, with the supplied encryption service.
Original KV, SQL, blob, selected-file, key-access, and complete artifact checks remain mandatory after completion.
Manifest-only inspection cannot waive original artifact retention.

The existing portable owners stream records and assets under their existing per-record limits.
Independent history remains bounded by 64 MiB of JSON and 16 GiB of streamed assets.
The projection has no whole-deployment scratch-byte quota measurement.
It does not claim support for an unbounded deployment or a numerical disk-capacity target.
A hard aggregate quota requires a coordinated copy and publication budget across all three owners.
Capacity measurements must include source copies, mutable images, final images, and temporary verification copies.

## Proposed application API

These names describe a proposed interface. They are not implemented entry points.

- `app.PreparePopulatedRecovery` derives the offline expected state and compares an independently captured, fenced live image.
- `app.ActivatePopulatedRecovery` consumes private native closed-adoption capabilities and exact original receipts.
- `app.InspectPopulatedRecovery` checks retained evidence without repair or target recapture.

Requests retain the original backup and complete independent history, original approval evidence, the explicit selected configuration, and one operation.
The application derives native storage and catalog identities through their owners.
Caller-selected digests or raw mutations cannot grant adoption.
Captured configuration and secrets cannot overwrite the current operator selections.

## Required native state sequence

1. Close SQL admission and stop ordinary gateway, worker, administrator, publisher, and former-primary writers.
2. Retain the independently known prior SQL approval and closed epoch.
3. Prove process or network fencing outside the software state machine.
4. Observe the selected current native identity through the actual KV owner.
5. Capture the complete KV, SQL, blob, and selected-file image while every writer remains fenced.
6. Derive expected domain state from the original backup and complete independent interval.
7. Compare the capture with that expected state and the original final revision preimages.
8. Get restricted populated-adoption claims through native owners, bound to the exact original image and operation.
9. Preserve every acknowledged record, uncertain attempt, expiration, historical receipt, and inactive catalog capsule.
10. Check target catalog selection and native publication identities through the catalog owner.
11. Rotate final KV and SQL authorization once under the closed native claims.
12. Approve an epoch above the independently known high water and bind every owner to the same current native identity.
13. Release exact verified barriers, then start a fresh gateway and verify actual readiness before routing traffic.

The ordinary empty-target `Claim` guard must not change.
The native populated-adoption interface must not expose unrestricted `AdoptFleet` or permit erase, reset, or implicit reopening.
Missing acknowledged bytes, incomplete history, restored SQL ambiguity, changed targets, or failed owner checks remain restricted.
Known withdrawals take effect without a grace period.
Uncertain provider attempts remain held and never repeat automatically.

## Crash and authority requirements

Each phase retains original native identities, claims, cursors, preimages, receipts, and times before its effect.
Lost replies recover the exact original receipt.
Completed retries never select an older catalog or reopen a later closed epoch.
Externally enforced fencing remains active until fresh gateway readiness.
A string reference records evidence location and never proves that a writer stopped.
SQL closure alone does not fence generic KV or administrator writes.

## Separate partial restart qualification

The drafted test owns one exact pinned Valkey container and AOF volume.
It starts from an actual populated recovered application and records a complete persistent KV census.
It stops and restarts that same process with the same persisted data.
It checks changed native identity and unchanged persistent bytes.
It checks that stale authority and a fresh application refuse operation.
The actual operator command recovers into a separate empty owned target and checks fresh application state.

The old reachable native handle remains closed and cannot adopt the new epoch.

That flow does not qualify complete in-place populated adoption.
It does not prove promotion, acknowledged-data-loss recovery, restored SQL continuity, an HTTP readiness response, or physical fencing of every old writer.
Observed restart and recovery durations do not establish supported RPO or RTO targets.
`A33.restart_and_recovery_epoch` remains unqualified until the complete required contracts pass.
