# Fleet catalog retention

Status: local implementation pending final review and native CI.

Starmap owns automatic collection through its canonical retention settings. Disabling automatic cleanup preserves committed publications until explicit collection.
Publishing or accepting a catalog does not collect committed data. Recovery can still finish a prior deletion or remove an abandoned upload.
The library collection API remains available when the operator disables automatic cleanup.

Starport owns fleet payload retention in the Valkey adapter. Starmap owns acquisition and catalog selection.
Maintenance does not run on inference requests. Requests use the accepted in-memory catalog.

The adapter uses one durable inventory and one native maintenance lease per deployment.
Every payload mutation checks that lease in the same transaction. An expired owner cannot resume a write.
Each upload has a unique blob identity. Chunks never share ownership across uploads.

Before writing chunks, the inventory records the complete upload descriptor as pending.
Publication atomically clears that pending record, registers the immutable receipt, and changes the head.
The transaction also checks the original acquisition grant, native expiry, and exact predecessor.
An ambiguous result retains enough evidence to distinguish a committed receipt from an abandoned upload.

The next maintenance owner finishes any pending deletion or abandoned upload cleanup.
It first holds a new native lease, which fences the prior owner.
The pending descriptor remains durable until every chunk deletion completes.
No catalog chunk expires by time. An outage cannot expire the selected catalog bytes.

Collection protects the current publication, accepted publication, accepted rollback history, and active generation readers.
It also retains the most recent 32 publication receipts for exact retries. Explicit required generation IDs receive the same protection.

Configured generation and byte limits control other retained generations. Limits cannot remove protected content.
When protected content exceeds a limit, collection reports `OverLimit`.
An older retry whose receipt has left retention returns a conflict and cannot move the head.

A generation reader retains its bytes until explicit release. Reader claims do not expire with a host clock.
The adapter permits at most 256 claims, 96 retained publications, and 2 GiB of encoded retained and staged bytes.
It refuses another upload at those limits. Run explicit collection or release protected generations before retrying.
CSP13 owns inspection and fenced recovery of abandoned reader claims.

Collection removes an unprotected entry from the inventory before deleting its chunks.
The transaction compares the current and accepted heads. A changed acceptance forces a retry.
Pin acquisition and collection use the same maintenance protocol.
Existing inference snapshots remain valid independent copies of catalog data.

The pre-release fleet format requires its inventory whenever a durable head exists.
Missing or invalid inventory refuses operation. It never authorizes an empty-store reset.

Collection reports public catalog usage separately from publication storage. A public generation counts once, with its manifest and payload bytes.
Input-only updates can create several receipts for that generation. Publication accounting reports receipt count, encoded bytes, recovery bytes, and active reader claims.

Encoded bytes include pending reservations. Public generation bytes exclude private recovery inputs and backend replication.
Starmap copies this report into its in-memory retention status. Reading that status does not query storage.

A dry run returns the proposed result and preserves stored data, including pending cleanup.
The scan limit bounds the number of inspected inventory entries. The input byte limit bounds receipt and chunk reads for cleanup.

Collection reserves the maximum receipt read and complete chunk size before deletion. An insufficient limit refuses the pass before data changes.
Increase `catalog_retention.input_max_bytes` when the reported cleanup cannot fit the configured bound.
An interrupted deletion retains a durable pending record. The next collection can resume it.

The first publication atomically records that the catalog head exists.
A missing head after initialization requires recovery. It cannot authorize another embedded bootstrap.
Lease acquisition also compares the observed head in its native transaction.

Starmap version 2 recovery retains the complete reconstruction baseline independently of the binary.
Its compressed record reduces storage while bounding both encoded and decoded input bytes.
The stored recovery-byte count measures compressed bytes. The full publication count includes JSON framing and encoding overhead.
