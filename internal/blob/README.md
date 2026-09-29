# Blob storage and transfer

This package owns payload bytes and permanent retirement markers.
The file and job repositories own the records that refer to those bytes.

Both backends address objects by the SHA-256 digest of their logical key:

- Mutable bytes: `objects/<first-two>/<next-two>/<digest>`.
- Immutable bytes and retirement markers: `retained-v1/<first-two>/<next-two>/<digest>`.

A retained object starts with `SPBLOB1L` for live bytes or `SPBLOB1R` for retirement.
A retirement marker contains only its envelope. Backup retains markers even when no file or job record refers to them.
This prevents a delayed publisher from recreating retired bytes after recovery.

Object storage requires the `.starport/layout` receipt within its configured prefix.
The first operation creates this receipt only for an empty prefix.
A populated prefix without the receipt requires explicit migration.
Construction makes no network request. Successful layout checks remain in process memory.

`BackupLegacyObjects` exports the former logical-key object layout without changing the source.
Restore that image into a different directory or empty prefix.
Stop and fence all old writers before export. Do not run old and new writers against the same prefix.
There is no automatic fallback to the former layout.

`Backup` writes an exclusive private tar archive and returns its format, size, digest, object count, and retirement count.
It excludes incomplete uploads and noncurrent object versions.
The caller must stop and fence writers and coordinate the snapshot with KV, SQL, configuration, and recovery evidence.
The archive alone cannot establish deployment consistency or permission to resume admission.

Both restore methods verify the complete archive in private scratch storage before they change the destination.
Filesystem restore publishes a new directory and refuses existing paths.
Object-store restore claims an empty prefix, uses conditional writes, and verifies stored bytes.
An interrupted object-store import can resume only with the same operation identifier and image.
Conflicting bytes, unexpected objects, and different owners cause an error.

Restores retain `.starport/import` after success or partial failure.
New filesystem clients and fresh object-store layout checks refuse this barrier.
Previously opened clients can retain readiness state. The barrier does not replace external writer fencing.

## Component activation

The deployment recovery coordinator must verify all components, independent history, and external fencing before activation.
Native restore targets implement `ImportActivator.ActivateImport(ctx, operation, snapshot, decisionSHA256)`.
The operation and snapshot must match the completed import.
The recovery decision digest must identify the complete coordinator decision.
This component operation grants no permission to admit inference.

Activation retains a private current receipt and a historical receipt before it releases the barrier.
The current receipt moves from `preparing` to `active`.
Conditional replacement changes `.starport/import` into an activation record. Activation never deletes that control key.
Fresh startup accepts only a completed activation with matching current, historical, and import records.
Object storage also requires its layout receipt.

An exact retry verifies those receipts without copying or replacing later payloads.
A failure can follow a durable write. Preserve the target and retry the same activation.
Different decisions, changed ownership, missing evidence, and corrupt records cause refusal.
Restore commands refuse a target after activation starts. The coordinator must resume activation instead of repeating import.

The filesystem adapter uses private conditional file publication.
Exact activation retries recover abandoned publication journals through their owner.
Unknown or changed journal contents cause refusal and remain available for inspection.

The object adapter probes conditional creation and replacement on an owned temporary object before activation.
The pinned MinIO fixture does not enforce conditional deletion. Activation therefore uses conditional replacement and does not depend on deletion semantics.
Other services require their own native qualification. The runtime probe does not establish their durability or lifecycle guarantees.

Portable backups include historical receipts at `.starport/activation-<claim-sha256>`.
The archive object count includes these receipts. Their bounded canonical records bind the claim and recovery decision digests.
Backups exclude the native current receipt and import record.

Imported history cannot establish current activation authority. A later restore needs a new import operation and coordinator decision.
Retain current control records and historical receipts in object-storage lifecycle policies.

Conditional-write probes use random logical keys in the ordinary payload namespace.
A process exit can leave one small probe object. Backup preserves that object as ordinary bytes without granting authority.
Activation never scans or deletes earlier probe objects. Its cleanup addresses only its own random key.
