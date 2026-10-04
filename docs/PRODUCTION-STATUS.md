# Production status

The current storage adapters do not establish a complete production qualification.
Review these limits before using multiple replicas or enforcing strict spend budgets.

| Deployment | Current storage roles | Qualification limit |
| --- | --- | --- |
| `starport dev` | In-memory Badger and SQLite. Temporary catalog and upload files by default. | Explicit catalog-state and object-store settings can retain data. |
| One persistent process | Badger for KV records, SQLite for relational records, and local catalog and upload files. | Back up each required store. A KV backup alone does not restore the deployment. |
| Multiple replicas | Shared Valkey for KV records, shared PostgreSQL for relational records, and shared object storage for uploads. The fleet refuses MySQL. | Valkey alone does not share relational identity, audit records, or file bytes. A Valkey promotion can lose writes that only the old primary acknowledged. |

The fleet qualification covers Valkey 7.2.14 with PostgreSQL 16.15 as the replicated recipe.
Redis and MySQL support claims require their own compatibility evidence.
Do not infer strict budget enforcement across store loss or failover from adapter availability.
The production work must also qualify catalog authority, replica configuration, recovery, and complete request latency.

Read the [deployment recipes](site/architecture/recipes.md) for the durable owners and recovery limits of each target.
Read the [operator guide](OPERATOR-GUIDE.md) for existing settings and initialization commands.
Read the [performance document](PERFORMANCE.md) for current measurement limits.
