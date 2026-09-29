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

The deployment recovery coordinator must verify all components and independent history before it removes barriers.
Object-store activation must publish the layout receipt before removing its import barrier.
The transfer helpers do not approve deployment recovery.
