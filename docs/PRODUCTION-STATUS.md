# Production status

The current storage adapters do not establish a complete production qualification.
Review these limits before using multiple replicas or enforcing strict spend budgets.

| Deployment | Current storage roles | Qualification limit |
| --- | --- | --- |
| `starport dev` | In-memory Badger and SQLite. Temporary catalog and upload files by default. | Explicit catalog-state and object-store settings can retain data. |
| One persistent process | Badger for KV records, SQLite for relational records, and local catalog and upload files. | Back up each required store. A KV backup alone does not restore the deployment. |
| Multiple replicas | Shared Valkey and shared PostgreSQL or MySQL for their respective records. Shared object storage for uploads. | Valkey alone does not share relational identity, audit records, or file bytes. |

The production catalog work will qualify Valkey with PostgreSQL as the primary replicated recipe.
Redis and MySQL support claims require their own compatibility evidence.
Do not infer strict budget enforcement across store loss or failover from adapter availability.
The production work must also qualify catalog authority, replica configuration, recovery, and complete request latency.

Read the [operator guide](OPERATOR-GUIDE.md) for existing settings and initialization commands.
Read the [performance document](PERFORMANCE.md) for current measurement limits.
