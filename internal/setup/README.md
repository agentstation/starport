# Local setup

`Service.Initialize` creates one configuration file and one initial gateway API key.
It honors the selected configuration file and Badger directory, including independent parent directories.
It refuses existing or conflicting state.

Setup closes and verifies the staged Badger database before publishing it at the selected database path.
It publishes configuration last. A durable database binding blocks gateway startup until the configuration transaction settles.
The two roots do not need to share a filesystem. Each rename stays within its own parent directory.

`GuardLocalStorage` holds the database writer lock throughout a persistent local gateway's lifetime.
Application shutdown closes the stores before releasing this guard.
Valkey and in-memory development storage do not use this local guard.

## Files

`C` is the parent of the selected configuration file.
`D` is the parent of the selected Badger directory. `B` is that directory's basename.
These values follow explicit leaf overrides rather than assuming the default product roots.

| Path | Purpose and lifetime |
| --- | --- |
| `C/.starport-setup/.owner.lock` | Stable configuration transaction lock. Keep it after setup and rollback. |
| `C/.starport-setup/transaction.json` | Pending transaction, native identities, file receipts, and generated configuration. Remove it only after verified completion. |
| `D/.starport-setup-B/.owner.lock` | Stable exclusion between setup, rollback, and a running local gateway. |
| `D/.starport-setup-B/configuration.json` | Bind pending database work to its configuration selector. Keep it through interruptions. |
| `D/.starport-init-<uuid>/` | Private staged database or database isolated for rollback. Preserve changed or unrecognized contents. |
| `.record-publications/` below each record-writing directory | Starmap's native record publisher owns its lock, journals, and recovery. |

Transaction and configuration files can contain the security master key. Their access policy is owner-only.
The plaintext gateway API key stays in the process result. Neither the transaction journal nor configuration stores it.
Badger owns its internal file modes inside the private engine directory.

Setup limits its journal to 1 MiB and its database receipt to 64 files.
Each database file can contain at most 64 MiB. The full receipt covers at most 128 MiB.
These limits cover initialization and rollback, not normal database growth.

## Recovery

Retry initialization with the same selected paths to recover a pending transaction.
The retry checks native directory identities, file identities, sizes, modes, timestamps, and content hashes.
It preserves unknown files, changed files, malformed journals, and conflicting destinations.

Recovery can remove an empty interrupted preparation.
A preparation that stopped while writing Badger can contain state without a complete receipt.
Recovery preserves that directory and reports its path for operator inspection.

Recovery removes verified unpublished state before issuing a new initial key.
A completed configuration publication retains its key record. Recovery cannot reproduce the original one-time plaintext key.

Rollback isolates the database and verifies its initial records before deleting configuration.
A refused rollback restores the database without replacing another destination.
If restoration cannot finish, its journal and database remain available for recovery.
Cleanup removes only receipt-bound files and supports retries after partial deletion.

The package tests terminate child processes at publication, rollback, restoration, and cleanup boundaries.
They also cover concurrent writers, path overrides, cancellation, active storage, and changed state.
