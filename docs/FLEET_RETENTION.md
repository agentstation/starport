# Fleet catalog retention

Status: local implementation pending policy integration and review.

The final adapter must use Starmap retention settings, including disabled automatic cleanup and explicit collection.
Current publication-triggered cleanup does not yet meet that configuration contract. Do not qualify this implementation for production.

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
It also retains the most recent 32 publication receipts for exact retries.
An older retry whose receipt has left retention returns a conflict and cannot move the head.

A generation reader retains its bytes until explicit release. Reader claims do not expire with a host clock.
The adapter permits at most 256 claims, 96 retained publications, and 2 GiB of encoded retained and staged bytes.
It refuses another upload when protected content exhausts those limits.
CSP13 owns inspection and fenced recovery of abandoned reader claims.

Collection removes an unprotected entry from the inventory before deleting its chunks.
The transaction compares the current and accepted heads. A changed acceptance forces a retry.
Pin acquisition and collection use the same maintenance protocol.
Existing inference snapshots remain valid independent copies of catalog data.

The pre-release fleet format requires its inventory whenever a durable head exists.
Missing or invalid inventory refuses operation. It never authorizes an empty-store reset.
